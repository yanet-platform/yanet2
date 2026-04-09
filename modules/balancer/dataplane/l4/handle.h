#pragma once

#include <stddef.h>
#include <stdint.h>

struct packet;
struct worker_context;

void
balancer_handle_l4_ipv4(
	struct worker_context *context,
	struct packet **packets,
	size_t packets_count
);

void
balancer_handle_l4_ipv6(
	struct worker_context *context,
	struct packet **packets,
	size_t packets_count
);