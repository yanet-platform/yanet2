#include "handle.h"

#include "lib/dataplane/module/packet_front.h"

#include "context.h"

void
balancer_handle_icmp_ip4(
	struct worker_context *context,
	struct packet **packets,
	size_t packets_count
) {
	for (size_t i = 0; i < packets_count; i++) {
		packet_front_drop(context->packet_front, packets[i]);
	}
}

void
balancer_handle_icmp_ip6(
	struct worker_context *context,
	struct packet **packets,
	size_t packets_count
) {
	for (size_t i = 0; i < packets_count; i++) {
		packet_front_drop(context->packet_front, packets[i]);
	}
}