#include "common/likely.h"
#include <string.h>

#include "group.h"

/*
 * Maximum number of distinct VS IDs we track per batch.
 * Practically 1-5 in normal traffic; 16 covers even degenerate cases.
 * If exceeded, grouping is skipped (packets still processed correctly,
 * just not batched per-VS for ACL queries).
 */
#define MAX_GROUPS 16

/*
 * Rearrange vs_ids[] and order[] so that entries with the same vs_id
 * are contiguous. Uses a two-pass counting sort with a small group
 * table, giving O(n * k) time for k distinct VS IDs.
 *
 * Returns true if grouping succeeded, false if there were too many
 * distinct VS IDs (caller should fall back to per-packet processing).
 */
void
group_by_id(uint32_t *vs_ids, uint8_t *order, size_t count) {
	/* Pass 1: collect unique VS IDs and count per group. */
	uint32_t unique_vs[MAX_GROUPS];
	size_t counts[MAX_GROUPS];
	size_t ngroups = 0;

	for (size_t i = 0; i < count; ++i) {
		for (size_t g = 0; g < ngroups; ++g) {
			if (unique_vs[g] == vs_ids[i]) {
				counts[g]++;
				goto next;
			}
		}
		if (unlikely(ngroups == MAX_GROUPS)) {
			return;
		}
		unique_vs[ngroups] = vs_ids[i];
		counts[ngroups] = 1;
		ngroups++;
	next:;
	}

	/* Compute scatter offsets from prefix sums. */
	size_t offsets[MAX_GROUPS];
	offsets[0] = 0;
	for (size_t g = 1; g < ngroups; ++g) {
		offsets[g] = offsets[g - 1] + counts[g - 1];
	}

	/* Pass 2: scatter into temporary arrays. */
	uint32_t tmp_ids[count];
	uint8_t tmp_order[count];
	for (size_t i = 0; i < count; ++i) {
		for (size_t g = 0; g < ngroups; ++g) {
			if (unique_vs[g] == vs_ids[i]) {
				size_t pos = offsets[g]++;
				tmp_ids[pos] = vs_ids[i];
				tmp_order[pos] = order[i];
				break;
			}
		}
	}

	memcpy(vs_ids, tmp_ids, count * sizeof(uint32_t));
	memcpy(order, tmp_order, count * sizeof(uint8_t));
}