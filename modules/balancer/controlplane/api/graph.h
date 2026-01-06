#pragma once

#include "real.h"
#include "vs.h"
#include <stddef.h>

struct graph_real {
	struct relative_real_identifier identifier;
	uint16_t weight;
	bool enabled;
};

struct graph_vs {
	struct vs_identifier identifier;
	size_t real_count;
	struct graph_real *reals;
};

struct balancer_graph {
	size_t vs_count;
	struct graph_vs *vs;
};