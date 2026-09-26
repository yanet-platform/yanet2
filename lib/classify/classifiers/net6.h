#pragma once

#include "common/lpm.h"
#include "common/network.h"
#include "common/value.h"

#include "lib/classify/classify.h"

#include <stdint.h>
#include <string.h>

// Marks a high half region whose row needs the two dimensional lookup:
// the high half trie value carries the final result class directly for
// every other region, a marked value carries the dense row of the join
// table behind the mark. The trie stores its values shifted left by
// one, so the mark lives in the top value bit the trie preserves and
// the classes and dense rows stay below it.
#define FILTER_NET6_ROW_MARK 0x40000000u

/*
 * IPv6 network classifier: the two half longest prefix matches with
 * their join table.
 *
 * The struct is embedded by value in a consumer classifier; the halves
 * and the table die with the consumer through the free below.
 */
struct classify_attr_net6 {
	// The high half trie value is the final result class, or the dense
	// join table row behind FILTER_NET6_ROW_MARK; the table itself
	// holds only the rows referenced from the marked values.
	struct lpm hi;
	struct lpm lo;
	struct value_table comb;
};

// Releases the internals of an embedded IPv6 network classifier and
// zeroes it, so a destroy path is idempotent.
static inline void
classify_attr_net6_free(
	struct memory_context *memory_context, struct classify_attr_net6 *attr
) {
	(void)memory_context;

	lpm_free(&attr->hi);
	lpm_free(&attr->lo);
	value_table_free(&attr->comb);
	memset(attr, 0, sizeof(*attr));
}

/*
 * Resolves the class of every address of a batch through the two half
 * longest prefix matches.
 *
 * The addresses are whole IPv6 addresses, one per packet. An address
 * resolves through the high half alone unless its region row carries a
 * low half distinction, in which case the low half resolves the row of
 * the join table behind the mark.
 */
static inline void
classify_net6_lookup(
	const struct classify_attr_net6 *attr,
	const uint8_t *addrs,
	uint32_t *results,
	uint32_t count
) {
	for (uint32_t idx = 0; idx < count; ++idx) {
		const uint8_t *addr = addrs + idx * NET6_LEN;
		uint32_t hi = lpm8_lookup(&attr->hi, addr);
		if (!(hi & FILTER_NET6_ROW_MARK)) {
			results[idx] = hi;
			continue;
		}
		uint32_t lo = lpm8_lookup(&attr->lo, addr + 8);
		results[idx] = *value_table_get_ptr(
			&attr->comb, hi & ~FILTER_NET6_ROW_MARK, lo
		);
	}
}
