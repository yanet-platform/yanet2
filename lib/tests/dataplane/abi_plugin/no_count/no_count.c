#include "lib/dataplane/config/plugin_abi.h"
#include "lib/dataplane/module/module.h"

static void
nocountabihashtest_handle_packets(
	struct dp_worker *dp_worker,
	struct module_ectx *module_ectx,
	struct packet_front *packet_front
) {
	(void)dp_worker;
	(void)module_ectx;
	(void)packet_front;
}

static struct module nocountabihashtest_module = {
	.name = "nocountabihashtest",
	.handler = nocountabihashtest_handle_packets,
};

YANET_MODULE_EXPORT struct module *
new_module_nocountabihashtest() {
	return &nocountabihashtest_module;
}

// Fabricated ABI table: entities without the count symbol; never hash-compared.
__attribute__((visibility("default")))
const struct yanet_abi_entity yanet_module_abi_entities_v1[] = {
	{"fn synthetic-entity-never-compared", "not-a-real-hash"},
};
