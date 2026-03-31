#pragma once

#include <stddef.h>

#include "lib/controlplane/config/cp_module.h"
#include "lib/dataplane/module/module.h"

#include "common/big_array.h"

struct balancer_session_table;
struct filter;

struct module *
new_module_balancer();

struct balancer_packet_handler {
    struct cp_module cp_module;

    struct session_table *session_table;

    struct filter *ipv4_vs_matcher;
    struct filter *ipv6_vs_matcher;
    
    size_t first_ipv6_vs;
    size_t vs_count;
    struct big_array vs;

    size_t reals_count;
    struct big_array reals;

    bool ipv4_vs_matcher_reused;
    bool ipv6_vs_matcher_reused;
};

void
balancer_handle_packets(
	struct dp_worker *dp_worker,
	struct module_ectx *module_ectx,
	struct packet_front *packet_front
);
