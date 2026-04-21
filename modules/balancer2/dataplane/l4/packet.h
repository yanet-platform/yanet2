#pragma once

#include "types/real.h"
#include "types/vs.h"

struct packet_context {
	struct packet *packet;

	struct balancer_vs *matched_vs;
	struct balancer_vs_stats *matched_vs_stats;

	struct balancer_real *selected_real;
	struct balancer_real_stats *selected_real_stats;
};
