#pragma once

#include <stdbool.h>

#include "common/lpm.h"
#include "common/memory_address.h"
#include "common/value.h"

struct net6_classifier {
	struct lpm hi;
	struct lpm lo;
	struct value_table comb;
};

// Shared per-direction IPv6 half-address classification.
//
// The hi and lo tries are built over the union of the v6 networks of two
// local classifiers (a and b), so a single double-LPM walk serves both
// filters. The remap arrays are indexed by a union half-class and hold the
// corresponding local half-class of each classifier. remap_hi_* arrays
// have hi_count entries and remap_lo_* arrays have lo_count entries. The
// array pointers are shared-memory relative pointers.
struct net6_share_dir {
	struct lpm hi;
	struct lpm lo;
	uint32_t hi_count;
	uint32_t lo_count;
	uint32_t *remap_hi_a;
	uint32_t *remap_lo_a;
	uint32_t *remap_hi_b;
	uint32_t *remap_lo_b;
};

// Whether dir holds a built shared classification.
//
// filter_net6_share_init and filter_net6_share_dir_free both leave every
// field, including remap_hi_a, at zero when there is nothing built, so a
// NULL remap pointer is a structural "not built" rather than a stored flag.
static inline bool
net6_share_dir_is_built(const struct net6_share_dir *dir) {
	return ADDR_OF(&dir->remap_hi_a) != NULL;
}
