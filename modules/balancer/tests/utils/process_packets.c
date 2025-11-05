#include "process_packets.h"

#include "common/memory_address.h"
#include "lib/controlplane/config/econtext.h"
#include "lib/dataplane/config/zone.h"

struct dp_config;
struct counter_storage;

void
balancer_handle_packets(
	struct dp_worker *dp_worker,
	struct module_ectx *module_ectx,
	struct packet_front *packet_front
);

void
process_packets(
	struct cp_module *cp_module, struct packet_front *packet_front
) {
	struct module_ectx ctx;
	struct dp_worker worker;
	worker.idx = 0;
	SET_OFFSET_OF(&ctx.cp_module, cp_module);
	balancer_handle_packets(&worker, &ctx, packet_front);
}