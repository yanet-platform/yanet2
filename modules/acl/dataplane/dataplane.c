#include "dataplane.h"
#include "config.h"

#include <rte_ether.h>

#include <stdint.h>

#include "dataplane/module/module.h"
#include "dataplane/packet/packet.h"
#include "dataplane/worker.h"
#include "dataplane/worker/worker.h"
#include "fwstate/lookup.h"
#include "fwstate/sync.h"
#include "lib/dataplane/time/clock.h"
#include "logging/log.h"

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

	struct fwstate_config *fwstate_config = &acl_config->fwstate_cfg;
	struct fwstate_sync_config *sync_config = &fwstate_config->sync_config;
	fwmap_t *fw4state = ADDR_OF(&fwstate_config->fw4state);
	fwmap_t *fw6state = ADDR_OF(&fwstate_config->fw6state);
	fwmap_t *state_table = NULL;

	// Time in nanoseconds is sufficient for keeping state up to 500 years
	uint64_t now = tsc_clock_get_time_ns(&dp_worker->clock);

	/*
	 * There are two major options:
	 *  - process packets one by one
	 *  - process stages one by one
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
			state_table = fw4state;

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
			state_table = fw6state;

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

			enum sync_packet_direction push_sync_packet = SYNC_NONE;

			switch (target->action) {
			case ACL_ACTION_ALLOW:
				packet_front_output(packet_front, packet);
				break;
			case ACL_ACTION_DENY:
				packet_front_drop(packet_front, packet);
				break;
			case ACL_ACTION_CREATE_STATE:
				push_sync_packet = SYNC_INGRESS;
				break;
			case ACL_ACTION_CHECK_STATE:
				if (fwstate_check_state(
					    state_table,
					    packet,
					    now,
					    &push_sync_packet
				    )) {
					packet_front_output(
						packet_front, packet
					);
				} else {
					packet_front_drop(packet_front, packet);
				}
				break;
			}

			if (push_sync_packet != SYNC_NONE) {
				// Allocate a new packet for the sync frame
				struct packet *sync_pkt =
					worker_packet_alloc(dp_worker);
				if (unlikely(sync_pkt == NULL)) {
					LOG(ERROR,
					    "failed to allocate sync packet");
					continue;
				}
				if (unlikely(
					    fwstate_craft_state_sync_packet(
						    sync_config,
						    packet,
						    push_sync_packet,
						    sync_pkt
					    ) == -1
				    )) {
					worker_packet_free(sync_pkt);
					LOG(ERROR,
					    "failed to craft sync packet");
					continue;
				}

				packet_front_output(packet_front, sync_pkt);
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
