#pragma once

#include "common/big_array.h"
#include "common/btree/u64.h"

struct net6_fast_classifier_part {
	struct btree_u64 btree;
	uint64_t *to;
};

struct net6_fast_classifier {
	struct net6_fast_classifier_part high;
	struct net6_fast_classifier_part low;
	struct big_array comb;
	uint32_t mismatch_classifier;
};
