#pragma once

#include "common/hash_index.h"
#include "common/memory.h"
#include "common/network.h"
#include "common/value.h"

#include "lib/classify/classify.h"

#include <stdint.h>
#include <string.h>

/*
 * IPv6 address classifier over one exact-match hash index.
 *
 * The regions are the distinct configured addresses and the match is a
 * single hash probe on the sixteen exact address bytes, no prefix
 * semantics: an address resolves iff the ruleset names it.
 *
 * The struct is embedded by value in a consumer classifier; the index
 * and the region arrays die with the consumer through the free below.
 */
struct classify_attr_net6_hash {
	// The exact-match index: the slot value is the region identifier.
	struct hash_index index;
	// The class of an address the ruleset does not name.
	uint32_t miss_class;
	uint32_t region_count;
	// The sixteen address bytes per region, addressed through
	// SET_OFFSET_OF, so the equality probe of a lookup compares the
	// stored key of the slot.
	uint8_t *keys;
	// The class per region, addressed through SET_OFFSET_OF.
	uint32_t *classes;
};

// Releases the internals of an embedded hashed IPv6 address classifier
// and zeroes it, so a destroy path is idempotent.
static inline void
classify_attr_net6_hash_free(
	struct memory_context *memory_context,
	struct classify_attr_net6_hash *attr
) {
	hash_index_fini(&attr->index);

	uint8_t *keys = ADDR_OF(&attr->keys);
	if (keys != NULL) {
		memory_bfree(
			memory_context, keys, NET6_LEN * attr->region_count
		);
	}
	uint32_t *classes = ADDR_OF(&attr->classes);
	if (classes != NULL) {
		memory_bfree(
			memory_context,
			classes,
			sizeof(uint32_t) * attr->region_count
		);
	}

	memset(attr, 0, sizeof(*attr));
}

// The hash of one sixteen byte exact address: a byte pass of the same
// mixing at insert and lookup time.
static inline uint32_t
classify_net6_hash_key_hash(const uint8_t *key) {
	uint32_t hash = 0x45d9f3bu;
	for (uint32_t idx = 0; idx < NET6_LEN; ++idx) {
		hash = (hash ^ key[idx]) * 0x45d9f3bu;
	}
	hash ^= hash >> 15;
	return hash;
}

struct classify_net6_hash_lookup_ctx {
	const struct classify_attr_net6_hash *attr;
	uint8_t key[NET6_LEN];
};

static inline int
classify_net6_hash_eq(uint32_t region, const void *data) {
	const struct classify_net6_hash_lookup_ctx *ctx = data;
	const uint8_t *keys = ADDR_OF(&ctx->attr->keys);
	return memcmp(keys + region * NET6_LEN, ctx->key, NET6_LEN) == 0 ? 0
									 : 1;
}

// Resolves the class of one address, sixteen bytes as read from the
// packet header: a single exact-match probe, the miss class when the
// ruleset does not name the address.
static inline uint32_t
classify_net6_hash_lookup1(
	const struct classify_attr_net6_hash *attr, const uint8_t *addr
) {
	struct classify_net6_hash_lookup_ctx ctx;
	memcpy(ctx.key, addr, NET6_LEN);
	ctx.attr = attr;

	uint32_t region = hash_index_lookup(
		&attr->index,
		classify_net6_hash_key_hash(addr),
		classify_net6_hash_eq,
		&ctx
	);
	if (region == HASH_INDEX_INVALID) {
		return attr->miss_class;
	}
	return ADDR_OF(&attr->classes)[region];
}

/*
 * Resolves the class of every address of a batch through the exact
 * match.
 *
 * The addresses are whole IPv6 addresses, one per packet.
 */
static inline void
classify_net6_hash_lookup(
	const struct classify_attr_net6_hash *attr,
	const uint8_t *addrs,
	uint32_t *results,
	uint32_t count
) {
	for (uint32_t idx = 0; idx < count; ++idx) {
		results[idx] = classify_net6_hash_lookup1(
			attr, addrs + idx * NET6_LEN
		);
	}
}
