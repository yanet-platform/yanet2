#pragma once

#include "types/vs.h"

struct vs_matched_packet {
    struct packet *packet;
    struct balancer_vs *matched_vs;
    struct balancer_vs_stats *matched_vs_stats;
};