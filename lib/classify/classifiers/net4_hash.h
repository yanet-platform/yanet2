#pragma once

#include "common/hash_index.h"
#include "common/memory.h"
#include "common/network.h"
#include "common/value.h"

#include "lib/classify/classify.h"

#include <stdint.h>
#include <string.h>

// Largest prefix length of the IPv4 hashed network classifier.
#define CLASSIFY_NET4_HASH_LENGTH_MAX 32

/*
 * IPv4 network classifier over hashed exact prefixes.
 *
 * The class space is the same the range partition of the LPM variant
 * produces - one region per distinct match set - but the match resolves
 * through hash lookups instead of a trie walk: the configured prefixes
 * group by length, and an address probes its masked word against the
 * groups from the longest length to the shortest, so the first hit is
 * the longest prefix match. The state sync scopes name a handful of
 * prefixes with few distinct lengths, where the probe chain is short
 * and every step is one flat hash probe.
 *
 * The struct is embedded by value in a consumer classifier; the indexes
 * and the region arrays die with the consumer through the free below.
 */
struct classify_attr_net4_hash {
	// The distinct prefix lengths of the regions, descending: the probe
	// order of the lookup.
	uint32_t length_count;
	uint32_t lengths[CLASSIFY_NET4_HASH_LENGTH_MAX];
	// The class of an address matching no configured prefix.
	uint32_t miss_class;
	// One index per distinct length, keyed by the address masked to the
	// length; the slot value is the region identifier.
	struct hash_index index[CLASSIFY_NET4_HASH_LENGTH_MAX];
	uint32_t region_count;
	// The masked big-endian word per region, addressed through
	// SET_OFFSET_OF, so the equality probe of a lookup compares the
	// stored key of the slot.
	uint32_t *keys;
	// The class per region, addressed through SET_OFFSET_OF.
	uint32_t *classes;
};

// Releases the internals of an embedded hashed IPv4 network classifier
// and zeroes it, so a destroy path is idempotent.
static inline void
classify_attr_net4_hash_free(
	struct memory_context *memory_context,
	struct classify_attr_net4_hash *attr
) {
	for (uint32_t idx = 0; idx < attr->length_count; ++idx) {
		hash_index_fini(&attr->index[idx]);
	}

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

// The hash of one masked address word: the same mixing at insert and
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
// packet header.
static inline uint32_t
classify_net4_hash_lookup1(
	const struct classify_attr_net4_hash *attr, uint32_t addr
) {
	// The mask applies to the address bytes, so the word reassembles in
	// host order first: an integer mask over the raw big-endian word
	// would keep the wrong bytes on a little-endian host.
	const uint8_t *bytes = (const uint8_t *)&addr;
	const uint32_t host = (uint32_t)bytes[0] << 24 |
			      (uint32_t)bytes[1] << 16 |
			      (uint32_t)bytes[2] << 8 | (uint32_t)bytes[3];

	for (uint32_t idx = 0; idx < attr->length_count; ++idx) {
		const uint32_t length = attr->lengths[idx];
		const uint32_t key =
			host & (length == 0 ? 0 : ~0u << (32 - length));

		struct classify_net4_hash_lookup_ctx ctx = {
			.attr = attr,
			.key = key,
		};
		uint32_t region = hash_index_lookup(
			&attr->index[idx],
			classify_net4_hash_key_hash(key),
			classify_net4_hash_eq,
			&ctx
		);
		if (region != HASH_INDEX_INVALID) {
			return ADDR_OF(&attr->classes)[region];
		}
	}
	return attr->miss_class;
}

/*
 * Resolves the class of every address of a batch through the hashed
 * prefix match.
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
