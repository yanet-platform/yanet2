#include "lib/dataplane/config/plugin_abi.h"
#include "lib/dataplane/module/module.h"

static void
unknownabihashtest_handle_packets(
	struct dp_worker *dp_worker,
	struct module_ectx *module_ectx,
	struct packet_front *packet_front
) {
	(void)dp_worker;
	(void)module_ectx;
	(void)packet_front;
}

static struct module unknownabihashtest_module = {
	.name = "unknownabihashtest",
	.handler = unknownabihashtest_handle_packets,
};

YANET_MODULE_EXPORT struct module *
new_module_unknownabihashtest() {
	return &unknownabihashtest_module;
}

// Fabricated ABI table: an entity this dataplane never exported.
__attribute__((visibility("default")))
const struct yanet_abi_entity yanet_module_abi_entities_v1[] = {
	{"fn yanet_test_removed_entity_does_not_exist",
	 "0000000000000000000000000000000000000000000000000000000000000000"},
};
__attribute__((visibility("default"))) const size_t yanet_module_abi_count_v1 =
	1;
