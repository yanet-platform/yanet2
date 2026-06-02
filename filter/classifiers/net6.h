#pragma once

#include "common/lpm_wide.h"
#include "common/value.h"

struct net6_classifier {
	struct lpm_wide hi;
	struct lpm_wide lo;
	struct value_table comb;
};