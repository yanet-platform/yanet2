#pragma once

#include "common/fast_lpm.h"
#include "common/value.h"

struct net6_classifier {
	struct fast_lpm hi;
	struct fast_lpm lo;
	struct value_table comb;
};
