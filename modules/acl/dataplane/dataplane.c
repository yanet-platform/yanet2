#include "dataplane.h"
#include "config.h"

#include <rte_ether.h>

#include <stdint.h>
#include <stdlib.h>
#include <string.h>

#include "dataplane/worker.h"
#include "lib/controlplane/config/econtext.h"
#include "lib/dataplane/module/module.h"
#include "lib/dataplane/module/packet_front.h"
#include "lib/dataplane/packet/packet.h"
#include "lib/dataplane/pipeline/econtext.h"
#include "lib/dataplane/time/clock.h"
#include "lib/dataplane/worker/worker.h"
#include "lib/fwstate/lookup.h"
#include "lib/fwstate/sync.h"
#include "lib/logging/log.h"
#include "objects/fwstate/api/fwstate_map_v4_object.h"
#include "objects/fwstate/api/fwstate_map_v6_object.h"

#include <lib/filter2/query.h>

#include "filter_lookup.h"

struct acl_module {
	struct module module;
};

static void
acl_handle_packets(
	struct dp_worker *dp_worker,
	struct module_ectx *module_ectx,
	struct packet_front *packet_front
) {
	struct acl_module_config *acl_config = container_of(
		module_ectx->abs_cp_module, struct acl_module_config, cp_module
	);

	// fwtables of the linked map objects, one per family. NULL when the
	// config declared no link for the family, in which case CHECK_STATE
	// finds no state for that family.
	fwtable_t *fw4table = NULL;
	fwtable_t *fw6table = NULL;
	if (acl_config->v4_object_link_idx != ACL_OBJECT_LINK_NONE) {
		struct module_object_link_ectx *link = object_link_get_address(
			module_ectx, acl_config->v4_object_link_idx
		);
		if (link != NULL) {
			struct object_ectx *oectx = link->abs_object_ectx;
			struct cp_object *cp_obj = oectx->abs_cp_object;
			fw4table = fwstate_map_v4_object_table(cp_obj);
		}
	}
	if (acl_config->v6_object_link_idx != ACL_OBJECT_LINK_NONE) {
		struct module_object_link_ectx *link = object_link_get_address(
			module_ectx, acl_config->v6_object_link_idx
		);
		if (link != NULL) {
			struct object_ectx *oectx = link->abs_object_ectx;
			struct cp_object *cp_obj = oectx->abs_cp_object;
			fw6table = fwstate_map_v6_object_table(cp_obj);
		}
	}

	struct counter_storage *counter_storage =
		module_ectx->abs_counter_storage;

	struct counter_storage *rules_storage = module_ectx_counter_storage(
		module_ectx, acl_config->rules_registry_idx
	);

	// Rule counters are resolved per matched target; hoisting the lookup
	// array leaves a single hop per packet.
	struct counter_value_handle **rules_handles =
		ADDR_OF_NONNULL(&rules_storage->counter_value_handles);

	uint64_t *pass_cnt = counter_get_address(
		acl_config->action_allow_counter_id, counter_storage
	);

	uint64_t *deny_cnt = counter_get_address(
		acl_config->action_deny_counter_id, counter_storage
	);

	uint64_t *create_cnt = counter_get_address(
		acl_config->action_create_state_counter_id, counter_storage
	);

	uint64_t *check_pass_cnt = counter_get_address(
		acl_config->action_check_pass_counter_id, counter_storage
	);

	uint64_t *check_miss_cnt = counter_get_address(
		acl_config->action_check_miss_counter_id, counter_storage
	);

	uint64_t *sync_cnt = counter_get_address(
		acl_config->sync_sent_counter_id, counter_storage
	);

	uint64_t *invalid_cnt = counter_get_address(
		acl_config->action_invalid_counter_id, counter_storage
	);

	uint64_t *non_term_cnt = counter_get_address(
		acl_config->action_non_term_counter_id, counter_storage
	);

	uint64_t *no_match_cnt = counter_get_address(
		acl_config->no_match_counter_id, counter_storage
	);

	// Time in nanoseconds is sufficient for keeping state up to 500 years
	uint64_t now = dp_worker->current_time;

	/*
	 * There are two major options:
	 *  - process packets one by one
	 *  - process stages one by one
	 * For the second option we have to split v4 and v6 processing.
	 */

	// A force-polled tick can reach this handler with an empty front, and
	// zero-sizing every variable-length array declared below is undefined
	// behavior. The early return is only safe because everything below is
	// per-packet — trailing work added later must go above the guard.
	uint64_t count = packet_front_input_count(packet_front);
	if (count == 0) {
		return;
	}

	struct packet *vlan_packets[count];
	uint32_t vlan_result[count];
	uint64_t vlan_idx = 0;

	struct packet *ip4_packets[count];
	uint32_t ip4_result[count];
	uint64_t ip4_idx = 0;

	struct packet *ip4_port_packets[count];
	uint32_t ip4_port_result[count];
	uint64_t ip4_port_idx = 0;

	struct packet *ip6_packets[count];
	uint32_t ip6_result[count];
	uint64_t ip6_idx = 0;

	struct packet *ip6_port_packets[count];
	uint32_t ip6_port_result[count];
	uint64_t ip6_port_idx = 0;

	for (struct packet *packet = packet_list_first(&packet_front->input);
	     packet != NULL;
	     packet = packet->next) {

		vlan_packets[vlan_idx++] = packet;

		if (packet->network_header.type ==
		    rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4)) {
			ip4_packets[ip4_idx++] = packet;

			if (packet->fragment_offset == 0 &&
			    (packet->transport_header.type == IPPROTO_TCP ||
			     packet->transport_header.type == IPPROTO_UDP)) {
				ip4_port_packets[ip4_port_idx++] = packet;
			}
		}

		if (packet->network_header.type ==
		    rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6)) {
			ip6_packets[ip6_idx++] = packet;

			if (packet->fragment_offset == 0 &&
			    (packet->transport_header.type == IPPROTO_TCP ||
			     packet->transport_header.type == IPPROTO_UDP)) {
				ip6_port_packets[ip6_port_idx++] = packet;
			}
		}
	}

	filter_query(
		&acl_config->filter_vlan,
		acl_query_vlan,
		vlan_packets,
		vlan_result,
		vlan_idx
	);

	filter_query(
		&acl_config->filter_ip4,
		acl_query_ip4,
		ip4_packets,
		ip4_result,
		ip4_idx
	);

	filter_query(
		&acl_config->filter_ip4_port,
		acl_query_ip4_port,
		ip4_port_packets,
		ip4_port_result,
		ip4_port_idx
	);

	filter_query(
		&acl_config->filter_ip6,
		acl_query_ip6,
		ip6_packets,
		ip6_result,
		ip6_idx
	);

	filter_query(
		&acl_config->filter_ip6_port,
		acl_query_ip6_port,
		ip6_port_packets,
		ip6_port_result,
		ip6_port_idx
	);

	vlan_idx = 0;
	ip4_idx = 0;
	ip4_port_idx = 0;
	ip6_idx = 0;
	ip6_port_idx = 0;

	struct packet *packet;
	while ((packet = packet_list_pop(&packet_front->input)) != NULL) {
		struct acl_target *target = NULL;

		uint32_t action = vlan_result[vlan_idx];

		++vlan_idx;

		// State table for this packet: the linked object's fwtable for
		// its family, NULL for a non-IP packet or a family with no
		// link.
		fwtable_t *state_table = NULL;

		if (packet->network_header.type ==
		    rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4)) {
			state_table = fw4table;

			if (ip4_result[ip4_idx] < action) {
				action = ip4_result[ip4_idx];
			}

			++ip4_idx;

			if (packet->fragment_offset == 0 &&
			    (packet->transport_header.type == IPPROTO_TCP ||
			     packet->transport_header.type == IPPROTO_UDP)) {
				if (ip4_port_result[ip4_port_idx] < action) {
					action = ip4_port_result[ip4_port_idx];
				}
				++ip4_port_idx;
			}
		} else if (
			packet->network_header.type ==
			rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6)
		) {
			state_table = fw6table;

			if (ip6_result[ip6_idx] < action) {
				action = ip6_result[ip6_idx];
			}

			++ip6_idx;

			if (packet->fragment_offset == 0 &&
			    (packet->transport_header.type == IPPROTO_TCP ||
			     packet->transport_header.type == IPPROTO_UDP)) {
				if (ip6_port_result[ip6_port_idx] < action) {
					action = ip6_port_result[ip6_port_idx];
				}
				++ip6_port_idx;
			}
		}

		if (action != FILTER_RULE_INVALID) {
			target = ADDR_OF(&acl_config->targets) + action;
		}

		const uint64_t pkt_len = packet_data_len(packet);

		if (target != NULL) {
			enum sync_packet_direction push_sync_packet = SYNC_NONE;
			bool allow = false;

			for (uint64_t action_idx = 0;
			     action_idx < target->action_count;
			     ++action_idx) {
				switch (target->actions[action_idx]) {
				case ACTION_ALLOW: {
					allow = true;
					goto apply;
				}
				case ACTION_DENY: {
					goto apply;
				}
				case ACTION_COUNT: {
					uint64_t *counters =
						counter_handle_get_value(
							ADDR_OF_NONNULL(
								rules_handles +
								target->counter_id
							)
						);
					counters[0] += 1;
					counters[1] += pkt_len;

					break;
				}
				case ACTION_CREATE_STATE: {
					push_sync_packet = SYNC_INGRESS;
					break;
				}
				case ACTION_CHECK_STATE: {
					// A NULL table (non-IP packet, or a
					// family with no link) simply reports
					// no state.
					bool state_found =
						fwstate_check_state_table(
							state_table,
							packet,
							now,
							&push_sync_packet
						);
					if (state_found) {
						allow = true;
						check_pass_cnt[0] += 1;
						goto apply;
					} else {
						check_miss_cnt[0] += 1;
					}
					break;
				}
				case ACTION_LOG: {
					break;
				}
				default: {
					invalid_cnt[0] += 1;
					allow = false;
					goto apply;
				}
				}
			}

			/*
			 * There is no terminting action - the packet is
			 * going to be dropped.
			 */
			non_term_cnt[0] += 1;

		apply:

			if (!allow) {
				deny_cnt[0] += 1;
				packet_front_drop(packet_front, packet);
				continue;
			}

			/*
			 * Pass counter is increased in case of successful
			 * state checking what is ok as this implies a packet
			 * allowing.
			 */
			pass_cnt[0] += 1;
			packet_front_output(packet_front, packet);

			if (push_sync_packet != SYNC_NONE) {
				create_cnt[0] += 1;

				struct packet *sync_pkt =
					worker_packet_alloc(dp_worker);
				if (unlikely(sync_pkt == NULL)) {
					LOG(ERROR,
					    "failed to allocate sync packet");
					continue;
				}
				if (unlikely(
					    fwstate_craft_state_sync_packet(
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
				sync_pkt->flags |=
					1U << PACKET_FLAG_FWSTATE_SYNC_INTERNAL;

				sync_cnt[0] += 1;
				sync_cnt[1] += packet_data_len(sync_pkt);
				packet_front_output(packet_front, sync_pkt);
			}
		} else {
			no_match_cnt[0] += 1;

			packet_front_drop(packet_front, packet);
		}
	}
}

static void
acl_module_commit(struct dp_config *dp_config, struct cp_module *cp_module) {
	(void)dp_config;
	(void)cp_module;
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
	module->module.commit_handler = acl_module_commit;

	return &module->module;
}
