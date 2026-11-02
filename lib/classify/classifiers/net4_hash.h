#pragma once

#include "common/hash_index.h"
#include "common/memory.h"
#include "common/network.h"
#include "common/value.h"

#include "lib/classify/classify.h"

#include <stdint.h>
#include <string.h>

/*
 * IPv4 address classifier over one exact-match hash index.
 *
 * The class space matches the region model of the library - one region
 * per distinct match set - but the regions are the distinct configured
 * addresses and the match is a single hash probe on the exact address,
 * no prefix semantics: an address resolves iff the ruleset names it.
 * The scope rules of state synchronization name individual hosts, where
 * a flat probe beats both a trie walk and a per-length probe chain.
 *
 * The struct is embedded by value in a consumer classifier; the index
 * and the region arrays die with the consumer through the free below.
 */
struct classify_attr_net4_hash {
	// The exact-match index: the slot value is the region identifier.
	struct hash_index index;
	// The class of an address the ruleset does not name.
	uint32_t miss_class;
	uint32_t region_count;
	// The exact address per region as a host-order word, addressed
	// through SET_OFFSET_OF, so the equality probe of a lookup compares
	// the stored key of the slot.
	uint32_t *keys;
	// The class per region, addressed through SET_OFFSET_OF.
	uint32_t *classes;
};

// Releases the internals of an embedded hashed IPv4 address classifier
// and zeroes it, so a destroy path is idempotent.
static inline void
classify_attr_net4_hash_free(
	struct memory_context *memory_context,
	struct classify_attr_net4_hash *attr
) {
	hash_index_fini(&attr->index);

	uint32_t *keys = ADDR_OF(&attr->keys);
	if (keys != NULL) {
		memory_bfree(
			memory_context,
			keys,
			sizeof(uint32_t) * attr->region_count
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

// The hash of one exact address word: the same mixing at insert and
// lookup time, so an ordinary multiplicative pass suffices.
static inline uint32_t
classify_net4_hash_key_hash(uint32_t key) {
	key ^= key >> 16;
	key *= 0x45d9f3bu;
	key ^= key >> 16;
	key *= 0x45d9f3bu;
	key ^= key >> 16;
	return key;
}

struct classify_net4_hash_lookup_ctx {
	const struct classify_attr_net4_hash *attr;
	uint32_t key;
};

static inline int
classify_net4_hash_eq(uint32_t region, const void *data) {
	const struct classify_net4_hash_lookup_ctx *ctx = data;
	const uint32_t *keys = ADDR_OF(&ctx->attr->keys);
	return keys[region] == ctx->key ? 0 : 1;
}

// Resolves the class of one address, a big-endian word as read from the
// packet header: a single exact-match probe, the miss class when the
// ruleset does not name the address.
//
// The word reassembles in host order first: the probe compares integer
// keys, and the raw big-endian word would compare the wrong bytes on a
// little-endian host.
static inline uint32_t
classify_net4_hash_lookup1(
	const struct classify_attr_net4_hash *attr, uint32_t addr
) {
	const uint8_t *bytes = (const uint8_t *)&addr;
	const uint32_t key = (uint32_t)bytes[0] << 24 |
			     (uint32_t)bytes[1] << 16 |
			     (uint32_t)bytes[2] << 8 | (uint32_t)bytes[3];

	struct classify_net4_hash_lookup_ctx ctx = {
		.attr = attr,
		.key = key,
	};
	uint32_t region = hash_index_lookup(
		&attr->index,
		classify_net4_hash_key_hash(key),
		classify_net4_hash_eq,
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
 * The addresses are one big-endian word per packet, as read from the
 * packet header.
 */
static inline void
classify_net4_hash_lookup(
	const struct classify_attr_net4_hash *attr,
	const uint32_t *addrs,
	uint32_t *results,
	uint32_t count
) {
	for (uint32_t idx = 0; idx < count; ++idx) {
		results[idx] = classify_net4_hash_lookup1(attr, addrs[idx]);
	}
}
