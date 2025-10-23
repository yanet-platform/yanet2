#include "handle_packets.h"

#include <stdlib.h>

struct dp_config;
struct counter_storage;

void
balancer_handle_packets(
	struct dp_config *dp_config,
	uint64_t worker_idx,
	struct cp_module *cp_module,
	struct counter_storage *counter_storage,
	struct packet_front *packet_front
);

void
handle_packets(struct cp_module *cp_module, struct packet_front *packet_front) {
	balancer_handle_packets(NULL, 0, cp_module, NULL, packet_front);
}