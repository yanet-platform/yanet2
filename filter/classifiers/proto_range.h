#pragma once

#include "common/value.h"
#include "segments.h"

struct proto_range_classifier {
	struct value_table table;
};

struct proto_range_fast_classifier {
	struct segment_u16_classifier classifier;
};