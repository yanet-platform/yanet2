#pragma once

#include "real.h"
#include "vs.h"

struct packet_context {
	struct packet *packet;

	struct virtual_service *matched_vs;
	struct balancer_vs_stats *matched_vs_stats;

	struct real *selected_real;
	struct balancer_real_stats *selected_real_stats;
};
