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
#include "lib/fwstate/stash.h"
#include "lib/fwstate/stash_ectx.h"
#include "lib/fwstate/sync.h"
#include "lib/logging/log.h"
#include "objects/fwstate/api/fwstate_map_v4_object.h"
#include "objects/fwstate/api/fwstate_map_v6_object.h"

#include "filter_lookup.h"

struct acl_module {
	struct module module;
};

// Link of a packet with no state family: no table and no stash.
static const struct fwstate_map_link acl_no_state = {0};

// Append a sync record for an allowed packet to the executing worker's
// stash slot of the family map.
//
// Nothing is written without a linked map. A full slot discards the record
// and counts the overflow; the verdict does not depend on either outcome.
static inline void
acl_stash_sync_record(
	struct dp_worker *dp_worker,
	struct acl_prepared *prepared,
	const struct fwstate_stash_link *stash,
	const struct packet *packet,
	enum sync_packet_direction direction
) {
	if (stash->slot == NULL) {
		return;
	}

	struct fwstate_stash_slot *slot = stash->slot;
	fwstate_stash_slot_sync_round(slot, *dp_worker->iterations);
	struct fwstate_sync_record *record =
		fwstate_stash_slot_next(slot, stash->records, stash->capacity);
	if (record == NULL) {
		prepared->sync_overflow_cnt[0] += 1;
		return;
	}
	if (fwstate_fill_sync_record(packet, direction, record) != 0) {
		return;
	}
	fwstate_stash_slot_commit(slot);

	prepared->sync_cnt[0] += 1;
	prepared->sync_cnt[1] += sizeof(struct fw_state_sync_frame);
}

static void
acl_handle_packets(
	struct dp_worker *dp_worker,
	struct module_ectx *module_ectx,
	struct packet_front *packet_front
) {
	struct acl_module_config *acl_config = container_of(
		module_ectx->abs_cp_module, struct acl_module_config, cp_module
	);

	// Everything the burst loop needs that does not depend on the
	// packets themselves — the module counters, the per-rule counter
	// handle array and the linked state tables — is derived per
	// worker by the execution-context commit handler.
	struct acl_prepared *prepared = module_ectx->abs_module_prepared;

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

	struct packet *l2_packets[count];
	uint32_t l2_result[count];
	uint64_t l2_idx = 0;

	struct packet *ip4_packets[count];
	uint32_t ip4_result[count];
	uint64_t ip4_idx = 0;

	struct packet *ip4_tcp_packets[count];
	uint32_t ip4_tcp_result[count];
	uint32_t ip4_tcp_pos[count];
	uint64_t ip4_tcp_idx = 0;

	struct packet *ip4_udp_packets[count];
	uint32_t ip4_udp_result[count];
	uint32_t ip4_udp_pos[count];
	uint64_t ip4_udp_idx = 0;

	struct packet *ip4_icmp_packets[count];
	uint32_t ip4_icmp_result[count];
	uint32_t ip4_icmp_pos[count];
	uint64_t ip4_icmp_idx = 0;

	struct packet *ip6_packets[count];
	uint32_t ip6_result[count];
	uint64_t ip6_idx = 0;

	struct packet *ip6_tcp_packets[count];
	uint32_t ip6_tcp_result[count];
	uint32_t ip6_tcp_pos[count];
	uint64_t ip6_tcp_idx = 0;

	struct packet *ip6_udp_packets[count];
	uint32_t ip6_udp_result[count];
	uint32_t ip6_udp_pos[count];
	uint64_t ip6_udp_idx = 0;

	struct packet *ip6_icmp_packets[count];
	uint32_t ip6_icmp_result[count];
	uint32_t ip6_icmp_pos[count];
	uint64_t ip6_icmp_idx = 0;

	for (struct packet *packet = packet_list_first(&packet_front->input);
	     packet != NULL;
	     packet = packet->next) {

		l2_packets[l2_idx++] = packet;

		if (packet->network_header.type ==
		    rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4)) {
			ip4_packets[ip4_idx++] = packet;

			if (packet->fragment_offset == 0) {
				// The position of the path packet inside
				// the family batch: the shared core
				// classes are gathered through it.
				uint32_t *pos;
				struct packet **pkts;
				uint64_t *idx;
				if (packet->transport_header.type ==
				    IPPROTO_TCP) {
					pos = ip4_tcp_pos;
					pkts = ip4_tcp_packets;
					idx = &ip4_tcp_idx;
				} else if (packet->transport_header.type ==
					   IPPROTO_UDP) {
					pos = ip4_udp_pos;
					pkts = ip4_udp_packets;
					idx = &ip4_udp_idx;
				} else if (packet->transport_header.type ==
						   IPPROTO_ICMP ||
					   packet->transport_header.type ==
						   IPPROTO_ICMPV6) {
					pos = ip4_icmp_pos;
					pkts = ip4_icmp_packets;
					idx = &ip4_icmp_idx;
				} else {
					continue;
				}
				pos[*idx] = ip4_idx - 1;
				pkts[(*idx)++] = packet;
			}
		}

		if (packet->network_header.type ==
		    rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6)) {
			ip6_packets[ip6_idx++] = packet;

			if (packet->fragment_offset == 0) {
				uint32_t *pos;
				struct packet **pkts;
				uint64_t *idx;
				if (packet->transport_header.type ==
				    IPPROTO_TCP) {
					pos = ip6_tcp_pos;
					pkts = ip6_tcp_packets;
					idx = &ip6_tcp_idx;
				} else if (packet->transport_header.type ==
					   IPPROTO_UDP) {
					pos = ip6_udp_pos;
					pkts = ip6_udp_packets;
					idx = &ip6_udp_idx;
				} else if (packet->transport_header.type ==
						   IPPROTO_ICMP ||
					   packet->transport_header.type ==
						   IPPROTO_ICMPV6) {
					pos = ip6_icmp_pos;
					pkts = ip6_icmp_packets;
					idx = &ip6_icmp_idx;
				} else {
					continue;
				}
				pos[*idx] = ip6_idx - 1;
				pkts[(*idx)++] = packet;
			}
		}
	}

	// The l2 filter sees the whole burst: a rule without networks
	// matches every packet of its devices regardless of the protocol
	// family, so its result is the base the family results merge into.
	acl_classify_l2(
		&acl_config->classifier_l2,
		module_ectx->abs_cm_index,
		&acl_config->filter_l2.rule_map,
		(const struct packet **)l2_packets,
		l2_result,
		l2_idx
	);

	// The family filters share their core classification: the core
	// classes are computed once per family batch, the fragment and
	// ports suffixes are evaluated on their own and combined with the
	// core classes through the root joints of the final filters.
	// A family batch can be empty; the class scratch arrays are
	// guarded against zero sized declarations.
	uint32_t core4_classes[ip4_idx ? ip4_idx : 1];
	uint32_t frag4_classes[ip4_idx ? ip4_idx : 1];
	uint32_t core4_path_classes[ip4_idx ? ip4_idx : 1];

	acl_classify_core4(
		&acl_config->classifier_core4,
		module_ectx->abs_cm_index,
		(const struct packet **)ip4_packets,
		core4_classes,
		ip4_idx
	);

	acl_classify_frag(
		&acl_config->classifier_frag4,
		(const struct packet **)ip4_packets,
		frag4_classes,
		ip4_idx
	);

	classify_combine(
		&acl_config->filter_ip4.root_joint,
		&acl_config->filter_ip4.rule_map,
		core4_classes,
		frag4_classes,
		ip4_result,
		ip4_idx
	);

	if (ip4_tcp_idx != 0) {
		for (uint64_t idx = 0; idx < ip4_tcp_idx; ++idx) {
			core4_path_classes[idx] =
				core4_classes[ip4_tcp_pos[idx]];
		}
		acl_classify_tcp4(
			&acl_config->classifier_ports4,
			&acl_config->classifier_tcp4,
			&acl_config->filter_ip4_tcp,
			core4_path_classes,
			(const struct packet **)ip4_tcp_packets,
			ip4_tcp_result,
			ip4_tcp_idx
		);
	}

	for (uint64_t idx = 0; idx < ip4_udp_idx; ++idx) {
		core4_path_classes[idx] = core4_classes[ip4_udp_pos[idx]];
	}
	acl_classify_udp4(
		&acl_config->classifier_ports4,
		&acl_config->filter_ip4_udp,
		core4_path_classes,
		(const struct packet **)ip4_udp_packets,
		ip4_udp_result,
		ip4_udp_idx
	);

	for (uint64_t idx = 0; idx < ip4_icmp_idx; ++idx) {
		core4_path_classes[idx] = core4_classes[ip4_icmp_pos[idx]];
	}
	acl_classify_icmp4(
		&acl_config->classifier_icmp4,
		&acl_config->filter_ip4_icmp,
		core4_path_classes,
		(const struct packet **)ip4_icmp_packets,
		ip4_icmp_result,
		ip4_icmp_idx
	);

	uint32_t core6_classes[ip6_idx ? ip6_idx : 1];
	uint32_t frag6_classes[ip6_idx ? ip6_idx : 1];
	uint32_t core6_path_classes[ip6_idx ? ip6_idx : 1];

	acl_classify_core6(
		&acl_config->classifier_core6,
		module_ectx->abs_cm_index,
		(const struct packet **)ip6_packets,
		core6_classes,
		ip6_idx
	);

	acl_classify_frag(
		&acl_config->classifier_frag6,
		(const struct packet **)ip6_packets,
		frag6_classes,
		ip6_idx
	);

	classify_combine(
		&acl_config->filter_ip6.root_joint,
		&acl_config->filter_ip6.rule_map,
		core6_classes,
		frag6_classes,
		ip6_result,
		ip6_idx
	);

	for (uint64_t idx = 0; idx < ip6_tcp_idx; ++idx) {
		core6_path_classes[idx] = core6_classes[ip6_tcp_pos[idx]];
	}
	acl_classify_tcp6(
		&acl_config->classifier_ports6,
		&acl_config->classifier_tcp6,
		&acl_config->filter_ip6_tcp,
		core6_path_classes,
		(const struct packet **)ip6_tcp_packets,
		ip6_tcp_result,
		ip6_tcp_idx
	);

	for (uint64_t idx = 0; idx < ip6_udp_idx; ++idx) {
		core6_path_classes[idx] = core6_classes[ip6_udp_pos[idx]];
	}
	acl_classify_udp6(
		&acl_config->classifier_ports6,
		&acl_config->filter_ip6_udp,
		core6_path_classes,
		(const struct packet **)ip6_udp_packets,
		ip6_udp_result,
		ip6_udp_idx
	);

	for (uint64_t idx = 0; idx < ip6_icmp_idx; ++idx) {
		core6_path_classes[idx] = core6_classes[ip6_icmp_pos[idx]];
	}
	acl_classify_icmp6(
		&acl_config->classifier_icmp6,
		&acl_config->filter_ip6_icmp,
		core6_path_classes,
		(const struct packet **)ip6_icmp_packets,
		ip6_icmp_result,
		ip6_icmp_idx
	);

	l2_idx = 0;
	ip4_idx = 0;
	ip4_tcp_idx = 0;
	ip4_udp_idx = 0;
	ip4_icmp_idx = 0;
	ip6_idx = 0;
	ip6_tcp_idx = 0;
	ip6_udp_idx = 0;
	ip6_icmp_idx = 0;

	struct packet *packet;
	while ((packet = packet_list_pop(&packet_front->input)) != NULL) {
		struct acl_target *target = NULL;

		// The action resolves to the first matching rule across the l2
		// filter and the family filters of the packet.
		uint32_t action = l2_result[l2_idx];

		++l2_idx;

		// State table and stash for this packet: the linked map of the
		// packet's family, an empty link for a non-IP packet.
		const struct fwstate_map_link *state = &acl_no_state;

		if (packet->network_header.type ==
		    rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4)) {
			state = &prepared->fw4;

			if (ip4_result[ip4_idx] < action) {
				action = ip4_result[ip4_idx];
			}

			++ip4_idx;

			if (packet->fragment_offset == 0) {
				const uint32_t *path_result;
				uint64_t *path_idx;
				if (packet->transport_header.type ==
				    IPPROTO_TCP) {
					path_result = ip4_tcp_result;
					path_idx = &ip4_tcp_idx;
				} else if (packet->transport_header.type ==
					   IPPROTO_UDP) {
					path_result = ip4_udp_result;
					path_idx = &ip4_udp_idx;
				} else if (packet->transport_header.type ==
						   IPPROTO_ICMP ||
					   packet->transport_header.type ==
						   IPPROTO_ICMPV6) {
					path_result = ip4_icmp_result;
					path_idx = &ip4_icmp_idx;
				} else {
					path_idx = NULL;
				}
				if (path_idx != NULL &&
				    path_result[*path_idx] < action) {
					action = path_result[*path_idx];
				}
				if (path_idx != NULL) {
					++(*path_idx);
				}
			}
		} else if (packet->network_header.type ==
			   rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6)) {
			state = &prepared->fw6;

			if (ip6_result[ip6_idx] < action) {
				action = ip6_result[ip6_idx];
			}

			++ip6_idx;

			if (packet->fragment_offset == 0) {
				const uint32_t *path_result;
				uint64_t *path_idx;
				if (packet->transport_header.type ==
				    IPPROTO_TCP) {
					path_result = ip6_tcp_result;
					path_idx = &ip6_tcp_idx;
				} else if (packet->transport_header.type ==
					   IPPROTO_UDP) {
					path_result = ip6_udp_result;
					path_idx = &ip6_udp_idx;
				} else if (packet->transport_header.type ==
						   IPPROTO_ICMP ||
					   packet->transport_header.type ==
						   IPPROTO_ICMPV6) {
					path_result = ip6_icmp_result;
					path_idx = &ip6_icmp_idx;
				} else {
					path_idx = NULL;
				}
				if (path_idx != NULL &&
				    path_result[*path_idx] < action) {
					action = path_result[*path_idx];
				}
				if (path_idx != NULL) {
					++(*path_idx);
				}
			}
		}

		if (action != CLASSIFY_RULE_INVALID) {
			target = acl_config->abs_targets + action;
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
					uint64_t *counters = counter_handle_get_value(
						ADDR_OF_NONNULL(
							prepared->rules_handles +
							target->counter_id
						)
					);
					counters[0] += 1;
					counters[1] += pkt_len;

					break;
				}
				case ACTION_CREATE_STATE: {
					// A non-initial fragment has no
					// transport header to derive the
					// state from; creation waits for the
					// fragment carrying the header.
					if ((packet->transport_header.type &
					     PACKET_TRANSPORT_HEADER_UNAVAILABLE
					    ) == 0) {
						push_sync_packet = SYNC_INGRESS;
					}
					break;
				}
				case ACTION_CHECK_STATE: {
					// A NULL table (non-IP packet, or a
					// family with no link) simply reports
					// no state.
					bool state_found =
						fwstate_check_state_table(
							state->table,
							packet,
							now,
							&push_sync_packet
						);
					if (state_found) {
						allow = true;
						prepared->check_pass_cnt[0] +=
							1;
						goto apply;
					} else {
						prepared->check_miss_cnt[0] +=
							1;
					}
					break;
				}
				case ACTION_LOG: {
					break;
				}
				default: {
					prepared->invalid_cnt[0] += 1;
					allow = false;
					goto apply;
				}
				}
			}

			/*
			 * There is no terminting action - the packet is
			 * going to be dropped.
			 */
			prepared->non_term_cnt[0] += 1;

		apply:

			if (!allow) {
				prepared->deny_cnt[0] += 1;
				packet_front_drop(packet_front, packet);
				continue;
			}

			/*
			 * Pass counter is increased in case of successful
			 * state checking what is ok as this implies a packet
			 * allowing.
			 */
			prepared->allow_cnt[0] += 1;
			packet_front_output(packet_front, packet);

			if (push_sync_packet != SYNC_NONE) {
				prepared->create_cnt[0] += 1;
				acl_stash_sync_record(
					dp_worker,
					prepared,
					&state->stash,
					packet,
					push_sync_packet
				);
			}
		} else {
			prepared->no_match_cnt[0] += 1;

			packet_front_drop(packet_front, packet);
		}
	}
}

// Fill the execution context's prepared buffer for the worker that runs
// the context. An unknown worker links no stash, so no sync record is
// written on that context.
static void
acl_module_commit_ectx(
	struct module_ectx *module_ectx, struct cp_module *cp_module
) {
	uint64_t worker_idx = fwstate_stash_worker_idx(module_ectx);
	struct acl_module_config *acl_config =
		container_of(cp_module, struct acl_module_config, cp_module);

	// An absent or zeroed buffer leaves every value at its absent
	// state: no state table, no rule counter handle array.
	struct acl_prepared *prepared = module_ectx->abs_module_prepared;
	if (prepared == NULL) {
		return;
	}

	struct counter_storage *counter_storage =
		module_ectx->abs_counter_storage;

	prepared->allow_cnt = counter_get_address(
		acl_config->action_allow_counter_id, counter_storage
	);
	prepared->deny_cnt = counter_get_address(
		acl_config->action_deny_counter_id, counter_storage
	);
	prepared->create_cnt = counter_get_address(
		acl_config->action_create_state_counter_id, counter_storage
	);
	prepared->check_pass_cnt = counter_get_address(
		acl_config->action_check_pass_counter_id, counter_storage
	);
	prepared->check_miss_cnt = counter_get_address(
		acl_config->action_check_miss_counter_id, counter_storage
	);
	prepared->sync_cnt = counter_get_address(
		acl_config->sync_sent_counter_id, counter_storage
	);
	prepared->sync_overflow_cnt = counter_get_address(
		acl_config->sync_overflow_counter_id, counter_storage
	);
	prepared->invalid_cnt = counter_get_address(
		acl_config->action_invalid_counter_id, counter_storage
	);
	prepared->non_term_cnt = counter_get_address(
		acl_config->action_non_term_counter_id, counter_storage
	);
	prepared->no_match_cnt = counter_get_address(
		acl_config->no_match_counter_id, counter_storage
	);

	// Rule counters are resolved per matched target; hoisting the
	// lookup array leaves a single hop per packet.
	struct counter_storage *rules_storage = module_ectx_counter_storage(
		module_ectx, acl_config->rules_registry_idx
	);
	prepared->rules_handles =
		(rules_storage != NULL)
			? ADDR_OF_NONNULL(&rules_storage->counter_value_handles)
			: NULL;

	// Linked map objects' fwtables and stashes, one per family. NULL when
	// the config declared no link for the family, in which case
	// CHECK_STATE finds no state and no sync record is written for that
	// family.
	struct cp_object *fw4object = NULL;
	struct cp_object *fw6object = NULL;
	if (acl_config->v4_object_link_idx != ACL_OBJECT_LINK_NONE) {
		struct module_object_link_ectx *link = object_link_get_address(
			module_ectx, acl_config->v4_object_link_idx
		);
		if (link != NULL) {
			fw4object = link->abs_object_ectx->abs_cp_object;
		}
	}
	if (acl_config->v6_object_link_idx != ACL_OBJECT_LINK_NONE) {
		struct module_object_link_ectx *link = object_link_get_address(
			module_ectx, acl_config->v6_object_link_idx
		);
		if (link != NULL) {
			fw6object = link->abs_object_ectx->abs_cp_object;
		}
	}
	fwstate_map_v4_object_link(fw4object, worker_idx, &prepared->fw4);
	fwstate_map_v6_object_link(fw6object, worker_idx, &prepared->fw6);
}

static void
acl_module_commit(struct dp_config *dp_config, struct cp_module *cp_module) {
	(void)dp_config;

	struct acl_module_config *acl_config =
		container_of(cp_module, struct acl_module_config, cp_module);

	acl_config->abs_targets = ADDR_OF(&acl_config->targets);
}

struct module *
new_module_acl() {
	struct acl_module *module =
		(struct acl_module *)malloc(sizeof(struct acl_module));

	if (module == NULL) {
		return NULL;
	}

	// The loader copies every field of the returned descriptor, so
	// heap garbage must not survive in the ones this constructor
	// leaves unset.
	memset(module, 0, sizeof(*module));

	snprintf(module->module.name, sizeof(module->module.name), "%s", "acl");
	module->module.handler = acl_handle_packets;
	module->module.commit_handler = acl_module_commit;
	module->module.commit_ectx_handler = acl_module_commit_ectx;
	module->module.prepared_size = sizeof(struct acl_prepared);

	return &module->module;
}
