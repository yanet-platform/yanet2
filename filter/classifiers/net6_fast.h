#pragma once

#include "common/value.h"
#include "common/btree/u64.h"

struct net6_fast_classifier {
    struct btree_u64 high;
    uint64_t *high_to;

    struct btree_u64 low;
    uint64_t *low_to;

    struct value_table comb;
};
