#pragma once

#include "../../filter.h"
#include <stdatomic.h>

////////////////////////////////////////////////////////////////////////////////

FILTER_DECLARE(
	filter_sign, &attribute_net4_dst, &attribute_port_dst, &attribute_proto
);

////////////////////////////////////////////////////////////////////////////////

struct common {
	atomic_int ready; // 0 = not ready, 1 = compiler done
	struct filter filter;
};