#include "lib/dataplane/module/module.h"

static void
missingabihashtest_handle_packets(
	struct dp_worker *dp_worker,
	struct module_ectx *module_ectx,
	struct packet_front *packet_front
) {
	(void)dp_worker;
	(void)module_ectx;
	(void)packet_front;
}

static struct module missingabihashtest_module = {
	.name = "missingabihashtest",
	.handler = missingabihashtest_handle_packets,
};

YANET_MODULE_EXPORT struct module *
new_module_missingabihashtest() {
	return &missingabihashtest_module;
}
