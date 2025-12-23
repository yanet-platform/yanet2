#pragma once

#include "common/lpm.h"
#include "common/value.h"

struct net6_classifier {
	struct lpm hi;
	struct lpm lo;
	struct value_table comb;
};