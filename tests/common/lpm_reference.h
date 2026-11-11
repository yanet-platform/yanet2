#pragma once

/*
 * The LPM range insert checked by the LPM test against the walk it replaced.
 *
 * Two trees take the same ranges, one by the insert and one by the
 * reference walk, each in an arena of the same budget; they must return the
 * same results and hold the same pages, split in the same order, with the
 * same values. Equal pages make every lookup equal, so no lookup is compared.
 */

#include <errno.h>
#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>
#include <stdlib.h>

#include "common/key.h"
#include "common/lpm.h"
#include "common/memory.h"
#include "common/memory_block.h"

// The largest arena budget of a tree, and the alignment that makes the
// allocations of both trees the same.
#define LPM_REFERENCE_ARENA (8 << 20)
#define LPM_REFERENCE_ALIGN (2 << 20)

/*
 * Compares the bound of the subtree at the key's position with a range end:
 * the key up to the position, then the fill byte, as the old walk compared.
 */
static inline int
reference_cmp_bound(
	uint8_t key_size,
	const uint8_t *key,
	uint8_t hop,
	uint8_t fill,
	const uint8_t *end
) {
	for (uint8_t idx = 0; idx < key_size; ++idx) {
		uint8_t byte = idx <= hop ? key[idx] : fill;
		if (byte != end[idx]) {
			return byte < end[idx] ? -1 : 1;
		}
	}
	return 0;
}

/*
 * Maps the range to the value by the walk that checks every visited slot
 * against both ends of the range: the insert before the boundary walk.
 */
static inline int
reference_insert(
	struct lpm *lpm,
	uint8_t key_size,
	const uint8_t *from,
	const uint8_t *to,
	uint32_t value
) {
	uint8_t key[LPM_KEY_SIZE_MAX] = {from[0]};
	struct lpm_page *pages[LPM_KEY_SIZE_MAX] = {lpm_page(lpm, 0)};
	uint8_t hop = 0;
	uint8_t max_hop = 0;

	while (1) {
		union lpm_value *stored_value = pages[hop]->values + key[hop];
		bool leaf = stored_value->value & LPM_VALUE_FLAG;
		bool from_in =
			reference_cmp_bound(key_size, key, hop, 0, from) >= 0;
		bool to_in =
			reference_cmp_bound(key_size, key, hop, 0xff, to) <= 0;
		bool covered = hop + 1 == key_size || (from_in && to_in);
		if (leaf && covered) {
			stored_value->value = LPM_VALUE_SET(value);
			// The next slot in key order, up to the range end.
			while (1) {
				key[hop]++;
				int cmp = reference_cmp_bound(
					key_size, key, hop, 0xff, to
				);
				uint8_t last = cmp > 0 ? to[hop] : 0xff;
				if (key[hop] != (uint8_t)(last + 1)) {
					break;
				}
				if (hop == 0) {
					return 0;
				}
				--hop;
			}
			continue;
		}
		if (leaf && lpm_new_page(lpm, stored_value)) {
			return -1;
		}
		++hop;
		key[hop] = hop > max_hop ? from[hop] : 0;
		max_hop = hop > max_hop ? hop : max_hop;
		pages[hop] = ADDR_OF(&stored_value->page);
	}
}

// The index of the page in its tree, or the page count for a foreign page.
static inline size_t
page_index(const struct lpm *lpm, const struct lpm_page *page) {
	for (size_t base = 0; base < lpm->page_count; base += LPM_CHUNK_SIZE) {
		// Chunks are separate blocks, so addresses are compared as
		// integers: pointers into different arrays are not ordered.
		uintptr_t offset =
			(uintptr_t)page - (uintptr_t)lpm_page(lpm, base);
		if (offset < LPM_CHUNK_SIZE * sizeof(struct lpm_page)) {
			return base + offset / sizeof(struct lpm_page);
		}
	}
	return lpm->page_count;
}

// Whether both trees hold the same pages, each of the same slots.
static inline bool
same_tree(const struct lpm *lpm, const struct lpm *reference) {
	if (lpm->page_count != reference->page_count) {
		return false;
	}
	// The index lookup the child links rely on finds every page, and
	// no page of the other tree.
	if (page_index(lpm, lpm_page(reference, 0)) != lpm->page_count) {
		return false;
	}
	for (size_t idx = 0; idx < lpm->page_count; ++idx) {
		if (page_index(lpm, lpm_page(lpm, idx)) != idx ||
		    page_index(reference, lpm_page(reference, idx)) != idx) {
			return false;
		}
		const union lpm_value *slots = lpm_page(lpm, idx)->values;
		const union lpm_value *expected =
			lpm_page(reference, idx)->values;
		for (size_t slot = 0; slot < 256; ++slot) {
			uint64_t value = slots[slot].value;
			if (value & LPM_VALUE_FLAG ||
			    expected[slot].value & LPM_VALUE_FLAG) {
				if (value != expected[slot].value) {
					return false;
				}
			} else if (page_index(
					   lpm, ADDR_OF(&slots[slot].page)
				   ) !=
				   page_index(
					   reference,
					   ADDR_OF(&expected[slot].page)
				   )) {
				return false;
			}
		}
	}
	return true;
}

// A tree in an arena of its own.
struct reference_tree {
	void *arena;
	struct block_allocator ba;
	struct memory_context mctx;
	struct lpm lpm;
};

/*
 * Inserts the packed ranges into a tree by the insert and into one by the
 * reference, each in an arena of the budget; -1 when they disagree.
 *
 * A range is its start and its end of the key size and a two-byte value. A
 * range whose start is past its end goes into the first tree alone and must
 * leave it as it was. The first failed insert, which must fail in both with
 * ENOMEM, ends the run; the page count it left is stored for the caller.
 */
static inline int
reference_compare(
	const uint8_t *ranges,
	size_t count,
	uint8_t key_size,
	size_t budget,
	size_t *failed_pages
) {
	static struct reference_tree trees[2];
	*failed_pages = 0;
	int init_rc[2];
	for (size_t idx = 0; idx < 2; ++idx) {
		struct reference_tree *tree = &trees[idx];
		if (tree->arena == NULL) {
			tree->arena = aligned_alloc(
				LPM_REFERENCE_ALIGN, LPM_REFERENCE_ARENA
			);
		}
		if (tree->arena == NULL) {
			return -1;
		}
		block_allocator_init(&tree->ba);
		block_allocator_put_arena(&tree->ba, tree->arena, budget);
		memory_context_init(&tree->mctx, "lpm_reference", &tree->ba);
		init_rc[idx] = lpm_init(&tree->lpm, &tree->mctx, "lpm");
	}
	struct lpm *lpm = &trees[0].lpm;
	struct lpm *reference = &trees[1].lpm;

	int rc = init_rc[0] == init_rc[1] ? 0 : -1;
	// A key size of 0 is refused and leaves the tree untouched.
	errno = 0;
	if (init_rc[0] == 0 &&
	    (lpm_insert(lpm, 0, ranges, ranges, 0) != -1 || errno != EINVAL)) {
		rc = -1;
	}
	for (size_t idx = 0; rc == 0 && init_rc[0] == 0 && idx < count; ++idx) {
		const uint8_t *from = ranges + idx * (2u * key_size + 2);
		const uint8_t *to = from + key_size;
		uint32_t value = (uint32_t)to[key_size] << 8 | to[key_size + 1];
		if (filter_key_cmp(key_size, from, to) > 0) {
			if (lpm_insert(lpm, key_size, from, to, value) != 0 ||
			    !same_tree(lpm, reference)) {
				rc = -1;
			}
			continue;
		}
		errno = 0;
		int insert_rc = lpm_insert(lpm, key_size, from, to, value);
		int insert_errno = errno;
		errno = 0;
		if (reference_insert(reference, key_size, from, to, value) !=
			    insert_rc ||
		    errno != insert_errno ||
		    (insert_rc != 0 && insert_errno != ENOMEM)) {
			rc = -1;
		} else if (insert_rc != 0) {
			*failed_pages = lpm->page_count;
			break;
		}
	}
	if (rc == 0 && init_rc[0] == 0 && !same_tree(lpm, reference)) {
		rc = -1;
	}

	for (size_t idx = 0; idx < 2; ++idx) {
		if (init_rc[idx] == 0) {
			lpm_free(&trees[idx].lpm);
		}
		memory_context_fini(&trees[idx].mctx);
	}
	return rc;
}
