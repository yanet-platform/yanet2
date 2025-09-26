#include "dataplane.h"
#include "config.h"

#include <rte_ether.h>
#include <rte_ip.h>
#include <rte_tcp.h>
#include <rte_udp.h>

#include <rte_mbuf.h>

#include "dataplane/module/module.h"

struct acl_module {
	struct module module;
};

int
acl_handle_v4(
	struct filter *filter,
	struct packet *packet,
	const uint32_t **actions,
	uint32_t *count
) {
	filter_query(filter, packet, actions, count);
	return 0;
}

int
acl_handle_v6(
	struct filter *filter,
	struct packet *packet,
	const uint32_t **actions,
	uint32_t *count
) {
	filter_query(filter, packet, actions, count);
	return 0;
}

static void
acl_handle_packets(
	struct dp_config *dp_config,
	uint64_t worker_idx,
	struct cp_module *cp_module,
	struct counter_storage *counter_storage,
	struct packet_front *packet_front
) {
	(void)dp_config;
	(void)worker_idx;
	(void)counter_storage;
	struct acl_module_config *acl_config =
		container_of(cp_module, struct acl_module_config, cp_module);

	struct filter *compiler = &acl_config->filter;

	/*
	 * There are two major options:
	 *  - process packets one by one
	 *  - process stages ony by one
	 * For the second option we have to split v4 and v6 processing.
	 */

	struct packet *packet;
	while ((packet = packet_list_pop(&packet_front->input)) != NULL) {
		const uint32_t *actions = NULL;
		uint32_t count = 0;
		if (packet->network_header.type ==
		    rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4)) {
			acl_handle_v4(compiler, packet, &actions, &count);
		} else if (packet->network_header.type ==
			   rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6)) {
			acl_handle_v6(compiler, packet, &actions, &count);
		} else {
			packet_front_output(packet_front, packet);
			continue;
		}

		for (uint32_t idx = 0; idx < count; ++idx) {
			fprintf(stderr, "act %d\n", actions[idx]);
			if (!(actions[idx] & ACTION_NON_TERMINATE)) {
				if (actions[idx] == 1) {
					packet_front_output(
						packet_front, packet
					);
				} else if (actions[idx] == 2) {
					packet_front_drop(packet_front, packet);
				}
			}
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
