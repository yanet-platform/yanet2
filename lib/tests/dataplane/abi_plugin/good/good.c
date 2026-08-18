#include "lib/dataplane/config/zone.h"
#include "lib/dataplane/module/module.h"
#include "lib/dataplane/module/packet_front.h"
#include "lib/dataplane/packet/decap.h"

static void
abihashtest_handle_packets(
	struct dp_worker *dp_worker,
	struct module_ectx *module_ectx,
	struct packet_front *packet_front
) {
	(void)module_ectx;

	uint64_t now = dp_worker->current_time;
	(void)now;

	struct packet *pkt = packet_front->input.first;
	if (pkt != NULL) {
		packet_decap(pkt);
	}
}

static struct module abihashtest_module = {
	.name = "abihashtest",
	.handler = abihashtest_handle_packets,
};

YANET_MODULE_EXPORT struct module *
new_module_abihashtest() {
	return &abihashtest_module;
}
