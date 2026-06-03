#pragma once

#include "common/value.h"
#include "segments.h"

struct net6_fast_classifier {
	struct segments_u64_classifier high;
	struct segments_u64_classifier low;
	struct value_table comb;
};
