#pragma once

#include "common/lpm.h"
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
//
// After a successful build the union tries are frozen into the packed
// arenas and the mutable tries are released; when packing cannot be
// served the mutable tries stay and the packed arenas are zero, which
// the query side uses as the walk-path selector.
struct net6_share_dir {
	struct lpm hi;
	struct lpm lo;
	struct lpm8_packed hi_packed;
	struct lpm8_packed lo_packed;
	uint32_t hi_count;
	uint32_t lo_count;
	uint32_t *remap_hi_a;
	uint32_t *remap_lo_a;
	uint32_t *remap_hi_b;
	uint32_t *remap_lo_b;
};
