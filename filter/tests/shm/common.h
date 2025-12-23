#pragma once

#include "filter/filter.h"

#include <stdatomic.h>

////////////////////////////////////////////////////////////////////////////////

struct common {
	atomic_int ready; // 0 = not ready, 1 = compiler done
	struct filter filter;
};