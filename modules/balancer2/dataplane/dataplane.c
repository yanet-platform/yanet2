#include <netinet/in.h>
#include <rte_ether.h>
#include <stdio.h>
#include <stdlib.h>

#include "lib/dataplane/config/zone.h"
#include "lib/dataplane/module/packet_front.h"
#include "lib/dataplane/pipeline/econtext.h"

#include "dataplane.h"

void
balancer2_handle_packets(
	struct dp_worker *dp_worker,
	struct module_ectx *module_ectx,
	struct packet_front *packet_front
) {
	(void)dp_worker;
	(void)module_ectx;
	(void)packet_front;
	// TODO
}

struct balancer_module {
	struct module module;
};

struct module *
new_module_balancer2() {
	struct balancer_module *module =
		(struct balancer_module *)malloc(sizeof(struct balancer_module)
		);

	if (module == NULL) {
		return NULL;
	}

	snprintf(
		module->module.name,
		sizeof(module->module.name),
		"%s",
		"balancer"
	);
	module->module.handler = balancer2_handle_packets;

	return &module->module;
}
