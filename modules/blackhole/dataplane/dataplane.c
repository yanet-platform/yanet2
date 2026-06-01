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

struct module *
new_module_blackhole() {
	struct module *module = (struct module *)malloc(sizeof(*module));

	if (module == NULL) {
		return NULL;
	}

	snprintf(module->name, sizeof(module->name), "%s", "blackhole");
	module->handler = blackhole_handle_packets;

	return module;
}
