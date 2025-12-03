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

FILTER_DECLARE(FWD_FILTER_VLAN_TAG, &attribute_device, &attribute_vlan);

FILTER_DECLARE(
	FWD_FILTER_IP4_TAG,
	&attribute_device,
	&attribute_vlan,
	&attribute_net4_src,
	&attribute_net4_dst,
	&attribute_proto_range,
);

FILTER_DECLARE(
	FWD_FILTER_IP4_PROTO_PORT_TAG,
	&attribute_device,
	&attribute_vlan,
	&attribute_net4_src,
	&attribute_net4_dst,
	&attribute_proto_range,
	&attribute_port_src,
	&attribute_port_dst
);

FILTER_DECLARE(
	FWD_FILTER_IP6_TAG,
	&attribute_device,
	&attribute_vlan,
	&attribute_net6_src,
	&attribute_net6_dst,
	&attribute_proto_range
);

FILTER_DECLARE(
	FWD_FILTER_IP6_PROTO_PORT_TAG,
	&attribute_device,
	&attribute_vlan,
	&attribute_net6_src,
	&attribute_net6_dst,
	&attribute_proto_range,
	&attribute_port_src,
	&attribute_port_dst
);

static void
acl_handle_packets(
	struct dp_worker *dp_worker,
	struct module_ectx *module_ectx,
	struct packet_front *packet_front
) {
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
		struct acl_target *target = NULL;

		const uint32_t *actions = NULL;
		uint32_t action_count = 0;

		const uint32_t *vlan_actions;
		uint32_t vlan_action_count;
		FILTER_QUERY(
			&acl_config->filter_vlan,
			FWD_FILTER_VLAN_TAG,
			packet,
			&vlan_actions,
			&vlan_action_count
		);

		// Set vlan as default
		actions = vlan_actions;
		action_count = vlan_action_count;

		if (packet->network_header.type ==
		    rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4)) {
			const uint32_t *ip4_actions;
			uint32_t ip4_action_count;
			FILTER_QUERY(
				&acl_config->filter_ip4,
				FWD_FILTER_IP4_TAG,
				packet,
				&ip4_actions,
				&ip4_action_count
			);

			if (ip4_action_count && (action_count == 0 ||
						 ip4_actions[0] < actions[0])) {
				actions = ip4_actions;
				action_count = ip4_action_count;
			}

			if (packet->transport_header.type == IPPROTO_TCP ||
			    packet->transport_header.type == IPPROTO_UDP) {
				const uint32_t *ip4_port_actions;
				uint32_t ip4_port_action_count;
				FILTER_QUERY(
					&acl_config->filter_ip4_port,
					FWD_FILTER_IP4_PROTO_PORT_TAG,
					packet,
					&ip4_port_actions,
					&ip4_port_action_count
				);

				if (ip4_port_action_count &&
				    (action_count == 0 ||
				     ip4_port_actions[0] < actions[0])) {
					actions = ip4_port_actions;
					action_count = ip4_port_action_count;
				}
			}
		} else if (packet->network_header.type ==
			   rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6)) {
			const uint32_t *ip6_actions;
			uint32_t ip6_action_count;
			FILTER_QUERY(
				&acl_config->filter_ip6,
				FWD_FILTER_IP6_TAG,
				packet,
				&ip6_actions,
				&ip6_action_count
			);

			if (ip6_action_count && (action_count == 0 ||
						 ip6_actions[0] < actions[0])) {
				actions = ip6_actions;
				action_count = ip6_action_count;
			}

			if (packet->transport_header.type == IPPROTO_TCP ||
			    packet->transport_header.type == IPPROTO_UDP) {
				const uint32_t *ip6_port_actions;
				uint32_t ip6_port_action_count;
				FILTER_QUERY(
					&acl_config->filter_ip6_port,
					FWD_FILTER_IP6_PROTO_PORT_TAG,
					packet,
					&ip6_port_actions,
					&ip6_port_action_count
				);

				if (ip6_port_action_count &&
				    (action_count == 0 ||
				     ip6_port_actions[0] < actions[0])) {
					actions = ip6_port_actions;
					action_count = ip6_port_action_count;
				}
			}
		}

		if (action_count)
			target = ADDR_OF(&acl_config->targets) + actions[0];

		if (target != NULL) {
			uint64_t *counters = counter_get_address(
				target->counter_id,
				dp_worker->idx,
				ADDR_OF(&module_ectx->counter_storage)
			);
			counters[0] += 1;
			counters[1] += packet_data_len(packet);

			if (target->action == ACL_ACTION_ALLOW) {
				packet_front_output(packet_front, packet);
			} else {
				packet_front_drop(packet_front, packet);
			}

		} else {
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
