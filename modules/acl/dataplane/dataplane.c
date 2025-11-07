#include "dataplane.h"
#include "dataplane/module/module.h"
#include "module.h"

#include <rte_ether.h>
#include <rte_ip.h>
#include <rte_tcp.h>
#include <rte_udp.h>

#include <rte_mbuf.h>
#include <stdio.h>

#include "action.h"
#include "filter.h"

////////////////////////////////////////////////////////////////////////////////

struct acl_module {
	struct module module;
};

void
acl_handle_packets(
	struct dp_worker *dp_worker,
	struct module_ectx *module_ectx,
	struct packet_front *packet_front
) {
	(void)dp_worker;
	struct acl_module_config *acl_config = container_of(
		ADDR_OF(&module_ectx->cp_module),
		struct acl_module_config,
		cp_module
	);

	/*
	 * There are two major options:
	 *  - process packets one by one
	 *  - process stages ony by one
	 * For the second option we have to split v4 and v6 processing.
	 */

	struct packet *packet;
	while ((packet = packet_list_pop(&packet_front->input)) != NULL) {
		uint32_t *actions = NULL;
		uint32_t count = 0;
		if (packet->network_header.type ==
		    rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4)) {
			net4_filter_query(
				&acl_config->net4_filter,
				packet,
				&actions,
				&count
			);
		} else if (packet->network_header.type ==
			   rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6)) {
			net6_filter_query(
				&acl_config->net6_filter,
				packet,
				&actions,
				&count
			);
		} else {
			packet_front_output(packet_front, packet);
			continue;
		}

		int res = process_packet_actions(
			count, actions, packet, packet_front
		);
		if (res != 0) {
			// failed to process packet actions
			packet_front_drop(packet_front, packet);
		}
	}
}

struct module *
new_module_acl() {
	struct acl_module *module =
		(struct acl_module *)malloc(sizeof(struct acl_module));

	if (module == NULL) {
		return NULL;
	}

	snprintf(module->module.name, sizeof(module->module.name), "%s", "acl");
	module->module.handler = acl_handle_packets;

	return &module->module;
}
