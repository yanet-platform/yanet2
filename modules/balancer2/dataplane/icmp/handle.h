#pragma once

#include <stddef.h>

struct packet;
struct worker_context;

void
balancer_handle_icmp_ip4(
	struct worker_context *context,
	struct packet **packets,
	size_t packets_count
);

void
balancer_handle_icmp_ip6(
	struct worker_context *context,
	struct packet **packets,
	size_t packets_count
);