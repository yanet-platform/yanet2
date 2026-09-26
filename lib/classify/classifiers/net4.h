#pragma once

#include "common/lpm.h"
#include "common/value.h"

#include "lib/classify/classify.h"

#include <stdint.h>
#include <string.h>

/*
 * IPv4 network classifier: the longest prefix match over the address.
 *
 * The struct is embedded by value in a consumer classifier; the match
 * dies with the consumer through the free below.
 */
struct classify_attr_net4 {
	struct lpm lpm;
};

// Releases the internals of an embedded IPv4 network classifier and
// zeroes it, so a destroy path is idempotent.
static inline void
classify_attr_net4_free(
	struct memory_context *memory_context, struct classify_attr_net4 *attr
) {
	(void)memory_context;

	lpm_free(&attr->lpm);
	memset(attr, 0, sizeof(*attr));
}

/*
 * Resolves the class of every address of a batch through the longest
 * prefix match.
 *
 * The addresses are one big-endian word per packet, as read from the
 * packet header.
 */
static inline void
classify_net4_lookup(
	const struct classify_attr_net4 *attr,
	const uint32_t *addrs,
	uint32_t *results,
	uint32_t count
) {
	for (uint32_t idx = 0; idx < count; ++idx) {
		results[idx] =
			lpm4_lookup(&attr->lpm, (const uint8_t *)(addrs + idx));
	}
}
