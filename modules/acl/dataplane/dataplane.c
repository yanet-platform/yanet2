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

#include <lib/filter/query.h>

struct acl_module {
	struct module module;
};

FILTER_QUERY_DECLARE(filter_vlan, device, vlan);

FILTER_QUERY_DECLARE(
	filter_ip4, device, vlan, net4_src, net4_dst, ip_frag, proto_range
);

FILTER_QUERY_DECLARE(
	filter_ip4_port,
	device,
	vlan,
	net4_src,
	net4_dst,
	proto_range,
	port_src,
	port_dst
);

FILTER_QUERY_DECLARE(
	filter_ip6, device, vlan, net6_src, net6_dst, ip_frag, proto_range
);

FILTER_QUERY_DECLARE(
	filter_ip6_port,
	device,
	vlan,
	net6_src,
	net6_dst,
	proto_range,
	port_src,
	port_dst
);

// Position of net6_src/net6_dst within the attribute lists declared above:
// both filter_ip6 and filter_ip6_port put them right after device/vlan, at
// index 2 and 3. Keep these in sync with the two FILTER_QUERY_DECLARE
// calls — reordering either list moves the net6 leaf vertices the shared
// classification path reads.
#define ACL_FILTER_NET6_SRC_POS 2
#define ACL_FILTER_NET6_DST_POS 3

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

// Runs the filter_query classification pass with two leaf rows injected.
//
// Behaves exactly like filter_query, except that the leaf slot rows of the
// ext_src_lookup and ext_dst_lookup attributes are copied from the
// caller-supplied arrays instead of being computed by the per-filter leaf
// lookups. This intentionally duplicates filter_query's own inner-vertex
// reduction (lib/filter/query.h) rather than generalizing it to accept
// injected leaves — that header is shared by every module, and widening it
// is out of scope here. Keep the two reduction loops in sync by hand.
static void
acl_filter_query_ext(
	struct filter *filter,
	const struct filter_query *fq,
	struct packet **packets,
	uint32_t *results,
	uint32_t count,
	size_t ext_src_lookup,
	const uint32_t *ext_src_slots,
	size_t ext_dst_lookup,
	const uint32_t *ext_dst_slots
) {
	uint32_t slots[2 * MAX_ATTRIBUTES * count + 1];

	for (size_t ai = 0; ai < fq->lookup_count; ++ai) {
		size_t vtx = fq->lookup_count + ai;
		uint32_t *row = slots + vtx * count;
		if (ai == ext_src_lookup) {
			memcpy(row, ext_src_slots, sizeof(uint32_t) * count);
			continue;
		}
		if (ai == ext_dst_lookup) {
			memcpy(row, ext_dst_slots, sizeof(uint32_t) * count);
			continue;
		}
		const struct filter_vertex *v = &filter->v[vtx];
		fq->lookups[ai](ADDR_OF(&v->data), packets, row, count);
	}

	for (size_t vtx = fq->lookup_count - 1; vtx >= 2; --vtx) {
		struct filter_vertex *v = &filter->v[vtx];
		for (uint32_t idx = 0; idx < count; ++idx) {
			slots[vtx * count + idx] = value_table_get(
				&v->table,
				slots[(vtx << 1) * count + idx],
				slots[(vtx << 1 | 1) * count + idx]
			);
		}
	}

	const size_t root = fq->lookup_count > 1;
	struct filter_vertex *r = &filter->v[root];
	for (uint32_t idx = 0; idx < count; ++idx) {
		results[idx] = value_table_get(
			&r->table,
			root == 0 ? 0 : slots[(root << 1) * count + idx],
			slots[(root << 1 | 1) * count + idx]
		);
	}
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

	// When the compile side built the union tries, both v6 filters are
	// queried through them: each address half is classified once and the
	// union classes are translated per filter via the remap arrays.
	//
	// net6_share_src is left unbuilt in an ordinary running config
	// whenever the v6 ruleset does not split across both filter_ip6 and
	// filter_ip6_port — no v6 rules at all, every v6 rule port-scoped, or
	// every v6 rule left unscoped by port — and also when the
	// YANET_ACL_NET6_SHARE_DISABLE kill switch was set at control-plane
	// startup. A build failure inside acl_module_init_net6_share instead
	// rejects the whole config apply, exactly like a filter_ip6 or
	// filter_ip6_port build failure, so it is never one of the reasons a
	// running config reaches this point unbuilt. The else branch below is
	// therefore an ordinary production path, not dead code kept only for
	// the differential test.
	const bool net6_share =
		net6_share_dir_is_built(&acl_config->net6_share_src);

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

	// Position of each ip6_port batch packet within the ip6 batch, filled
	// only on the shared-classification path where every ip6_port packet
	// is by construction also an ip6 packet.
	uint32_t ip6_port_pos[count];

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
				if (net6_share) {
					ip6_port_pos[ip6_port_idx] =
						ip6_idx - 1;
				}
				ip6_port_packets[ip6_port_idx++] = packet;
			}
		}
	}

	filter_query(
		&acl_config->filter_vlan,
		filter_vlan,
		vlan_packets,
		vlan_result,
		vlan_idx
	);

	filter_query(
		&acl_config->filter_ip4,
		filter_ip4,
		ip4_packets,
		ip4_result,
		ip4_idx
	);

	filter_query(
		&acl_config->filter_ip4_port,
		filter_ip4_port,
		ip4_port_packets,
		ip4_port_result,
		ip4_port_idx
	);

	if (net6_share) {
		struct net6_share_dir *share_src = &acl_config->net6_share_src;
		struct net6_share_dir *share_dst = &acl_config->net6_share_dst;

		// Classify each v6 address half once on the union tries.
		uint32_t src_hi[count];
		uint32_t src_lo[count];
		uint32_t dst_hi[count];
		uint32_t dst_lo[count];

		for (uint64_t idx = 0; idx < ip6_idx; ++idx) {
			struct rte_mbuf *mbuf =
				packet_to_mbuf(ip6_packets[idx]);
			struct rte_ipv6_hdr *ipv6_hdr = rte_pktmbuf_mtod_offset(
				mbuf,
				struct rte_ipv6_hdr *,
				ip6_packets[idx]->network_header.offset
			);
			const uint8_t *saddr =
				(const uint8_t *)ipv6_hdr->src_addr;
			const uint8_t *daddr =
				(const uint8_t *)ipv6_hdr->dst_addr;

			src_hi[idx] = lpm8_lookup(&share_src->hi, saddr);
			src_lo[idx] = lpm8_lookup(&share_src->lo, saddr + 8);
			dst_hi[idx] = lpm8_lookup(&share_dst->hi, daddr);
			dst_lo[idx] = lpm8_lookup(&share_dst->lo, daddr + 8);
		}

		const uint32_t *src_hi_a = ADDR_OF(&share_src->remap_hi_a);
		const uint32_t *src_lo_a = ADDR_OF(&share_src->remap_lo_a);
		const uint32_t *dst_hi_a = ADDR_OF(&share_dst->remap_hi_a);
		const uint32_t *dst_lo_a = ADDR_OF(&share_dst->remap_lo_a);
		const uint32_t *src_hi_b = ADDR_OF(&share_src->remap_hi_b);
		const uint32_t *src_lo_b = ADDR_OF(&share_src->remap_lo_b);
		const uint32_t *dst_hi_b = ADDR_OF(&share_dst->remap_hi_b);
		const uint32_t *dst_lo_b = ADDR_OF(&share_dst->remap_lo_b);

		// Translate the union classes into the leaf classes of each
		// filter and combine them in the filter's own comb table.
		const size_t ip6_src_leaf =
			filter_ip6->lookup_count + ACL_FILTER_NET6_SRC_POS;
		const size_t ip6_dst_leaf =
			filter_ip6->lookup_count + ACL_FILTER_NET6_DST_POS;
		struct net6_classifier *ip6_src_cls = (struct net6_classifier *)
			ADDR_OF(&acl_config->filter_ip6.v[ip6_src_leaf].data);
		struct net6_classifier *ip6_dst_cls = (struct net6_classifier *)
			ADDR_OF(&acl_config->filter_ip6.v[ip6_dst_leaf].data);

		uint32_t ip6_src_slots[count];
		uint32_t ip6_dst_slots[count];

		for (uint64_t idx = 0; idx < ip6_idx; ++idx) {
			ip6_src_slots[idx] = value_table_get(
				&ip6_src_cls->comb,
				src_hi_a[src_hi[idx]],
				src_lo_a[src_lo[idx]]
			);
			ip6_dst_slots[idx] = value_table_get(
				&ip6_dst_cls->comb,
				dst_hi_a[dst_hi[idx]],
				dst_lo_a[dst_lo[idx]]
			);
		}

		const size_t ip6_port_src_leaf =
			filter_ip6_port->lookup_count + ACL_FILTER_NET6_SRC_POS;
		const size_t ip6_port_dst_leaf =
			filter_ip6_port->lookup_count + ACL_FILTER_NET6_DST_POS;
		struct net6_classifier *ip6_port_src_cls =
			(struct net6_classifier *)ADDR_OF(
				&acl_config->filter_ip6_port
					 .v[ip6_port_src_leaf]
					 .data
			);
		struct net6_classifier *ip6_port_dst_cls =
			(struct net6_classifier *)ADDR_OF(
				&acl_config->filter_ip6_port
					 .v[ip6_port_dst_leaf]
					 .data
			);

		uint32_t ip6_port_src_slots[count];
		uint32_t ip6_port_dst_slots[count];

		for (uint64_t idx = 0; idx < ip6_port_idx; ++idx) {
			uint32_t pos = ip6_port_pos[idx];
			ip6_port_src_slots[idx] = value_table_get(
				&ip6_port_src_cls->comb,
				src_hi_b[src_hi[pos]],
				src_lo_b[src_lo[pos]]
			);
			ip6_port_dst_slots[idx] = value_table_get(
				&ip6_port_dst_cls->comb,
				dst_hi_b[dst_hi[pos]],
				dst_lo_b[dst_lo[pos]]
			);
		}

		acl_filter_query_ext(
			&acl_config->filter_ip6,
			filter_ip6,
			ip6_packets,
			ip6_result,
			ip6_idx,
			ACL_FILTER_NET6_SRC_POS,
			ip6_src_slots,
			ACL_FILTER_NET6_DST_POS,
			ip6_dst_slots
		);
		acl_filter_query_ext(
			&acl_config->filter_ip6_port,
			filter_ip6_port,
			ip6_port_packets,
			ip6_port_result,
			ip6_port_idx,
			ACL_FILTER_NET6_SRC_POS,
			ip6_port_src_slots,
			ACL_FILTER_NET6_DST_POS,
			ip6_port_dst_slots
		);
	} else {
		filter_query(
			&acl_config->filter_ip6,
			filter_ip6,
			ip6_packets,
			ip6_result,
			ip6_idx
		);

		filter_query(
			&acl_config->filter_ip6_port,
			filter_ip6_port,
			ip6_port_packets,
			ip6_port_result,
			ip6_port_idx
		);
	}

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

			if (packet->fragment_offset == 0 &&
			    (packet->transport_header.type == IPPROTO_TCP ||
			     packet->transport_header.type == IPPROTO_UDP)) {
				if (ip4_port_result[ip4_port_idx] < action) {
					action = ip4_port_result[ip4_port_idx];
				}
				++ip4_port_idx;
			}
		} else if (packet->network_header.type ==
			   rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6)) {
			state = &prepared->fw6;

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
