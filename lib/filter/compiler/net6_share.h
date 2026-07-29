#pragma once

#include "common/memory.h"

#include "lib/errors/errors.h"
#include "lib/filter/classifiers/net6.h"
#include "lib/filter/rule.h"

// Builds a shared per-direction IPv6 half-classification.
//
// rules is the union projection over the rule sets of the two local
// classifiers, an array with NULL holes following the filter_init
// convention. is_src selects the source or destination address attribute.
// local_a and local_b are the per-filter classifiers whose half-partitions
// the union partition refines. Everything is allocated from mctx. On
// failure everything already built is released, out is zeroed, -1 is
// returned, and err reports the failing step — allocation failure or a
// broken refinement invariant are distinguished in the message.
int
filter_net6_share_init(
	struct memory_context *mctx,
	const struct filter_rule **rules,
	uint32_t rule_count,
	int is_src,
	const struct net6_classifier *local_a,
	const struct net6_classifier *local_b,
	struct net6_share_dir *out,
	yanet_error **err
);

// Releases the union tries and remap arrays of a shared classification.
//
// Safe on an all-zero or fully built structure — filter_net6_share_init
// never leaves a genuinely partial one, since it zeroes out on every
// failure path. NULL remap pointers are skipped and the whole structure
// is zeroed afterwards.
void
filter_net6_share_dir_free(
	struct memory_context *mctx, struct net6_share_dir *dir
);
