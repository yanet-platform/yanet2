#include <stdio.h>
#include <stdlib.h>

#include "lib/dataplane/module/packet_front.h"
#include "lib/dataplane/packet/packet.h"

#include "dataplane.h"

static void
blackhole_handle_packets(
	struct dp_worker *dp_worker,
	struct module_ectx *module_ectx,
	struct packet_front *packet_front
) {
	(void)dp_worker;
	(void)module_ectx;

	struct packet *packet;
	while ((packet = packet_list_pop(&packet_front->input)) != NULL) {
		packet_front_drop(packet_front, packet);
	}
}

static void
blackhole_module_commit(
	struct dp_config *dp_config, struct cp_module *cp_module
) {
	(void)dp_config;
	(void)cp_module;
}

struct module *
new_module_blackhole() {
	struct module *module = (struct module *)malloc(sizeof(*module));

	if (module == NULL) {
		return NULL;
	}

	snprintf(module->name, sizeof(module->name), "%s", "blackhole");
	module->handler = blackhole_handle_packets;
	module->commit_handler = blackhole_module_commit;

	return module;
}
