#pragma once

#include "common/hash_index.h"
#include "common/memory.h"
#include "common/network.h"
#include "common/value.h"

#include "lib/classify/classify.h"

#include <stdint.h>
#include <string.h>

// Largest prefix length of the IPv6 hashed network classifier.
#define CLASSIFY_NET6_HASH_LENGTH_MAX 128

/*
 * IPv6 network classifier over hashed exact prefixes.
 *
 * The class space matches the two half LPM variant - one region per
 * distinct match set - but the match resolves through hash lookups:
 * the configured prefixes group by length, and an address probes its
 * sixteen masked bytes against the groups from the longest length to
 * the shortest, so the first hit is the longest prefix match.
 *
 * The struct is embedded by value in a consumer classifier; the indexes
 * and the region arrays die with the consumer through the free below.
 */
struct classify_attr_net6_hash {
	// The distinct prefix lengths of the regions, descending: the probe
	// order of the lookup.
	uint32_t length_count;
	uint32_t lengths[CLASSIFY_NET6_HASH_LENGTH_MAX];
	// The class of an address matching no configured prefix.
	uint32_t miss_class;
	// One index per distinct length, keyed by the address masked to the
	// length; the slot value is the region identifier.
	struct hash_index index[CLASSIFY_NET6_HASH_LENGTH_MAX];
	uint32_t region_count;
	// The sixteen masked bytes per region, addressed through
	// SET_OFFSET_OF, so the equality probe of a lookup compares the
	// stored key of the slot.
	uint8_t *keys;
	// The class per region, addressed through SET_OFFSET_OF.
	uint32_t *classes;
};

// Releases the internals of an embedded hashed IPv6 network classifier
// and zeroes it, so a destroy path is idempotent.
static inline void
classify_attr_net6_hash_free(
	struct memory_context *memory_context,
	struct classify_attr_net6_hash *attr
) {
	for (uint32_t idx = 0; idx < attr->length_count; ++idx) {
		hash_index_fini(&attr->index[idx]);
	}

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

// The hash of one sixteen byte masked address: a byte pass of the same
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
// packet header.
static inline uint32_t
classify_net6_hash_lookup1(
	const struct classify_attr_net6_hash *attr, const uint8_t *addr
) {
	uint8_t key[NET6_LEN];
	for (uint32_t idx = 0; idx < attr->length_count; ++idx) {
		const uint32_t length = attr->lengths[idx];
		const uint32_t whole_bytes = length / 8;
		const uint8_t tail_bits = length % 8;
		// The mask keeps the first length bits: the whole bytes pass
		// through, the first tail_bits of the tail byte survive and the
		// rest clear.
		memcpy(key, addr, whole_bytes);
		if (whole_bytes < NET6_LEN) {
			key[whole_bytes] =
				tail_bits == 0
					? 0
					: addr[whole_bytes] &
						  (0xffu << (8 - tail_bits));
			memset(key + whole_bytes + 1,
			       0,
			       NET6_LEN - whole_bytes - 1);
		}

		struct classify_net6_hash_lookup_ctx ctx;
		memcpy(ctx.key, key, NET6_LEN);
		ctx.attr = attr;

		uint32_t region = hash_index_lookup(
			&attr->index[idx],
			classify_net6_hash_key_hash(key),
			classify_net6_hash_eq,
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
