#include "dataplane.h"
#include "config.h"

#include <rte_ether.h>

#include <stdint.h>
#include <stdlib.h>
#include <string.h>

#include "lib/controlplane/config/econtext.h"
#include "lib/dataplane/module/module.h"
#include "lib/dataplane/module/packet_front.h"
#include "lib/dataplane/packet/packet.h"
#include "lib/dataplane/time/clock.h"
#include "lib/dataplane/worker/worker.h"
#include "lib/fwstate/lookup.h"
#include "lib/fwstate/sync.h"
#include "lib/logging/log.h"

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
		ADDR_OF(&module_ectx->cp_module),
		struct acl_module_config,
		cp_module
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

	struct fwstate_config *fwstate_config = &acl_config->fwstate_cfg;
	struct fwstate_sync_emit_config *sync_config = &acl_config->sync_config;
	fwmap_t *fw4state = ADDR_OF(&fwstate_config->fw4state);
	fwmap_t *fw6state = ADDR_OF(&fwstate_config->fw6state);

	struct counter_storage *counter_storage =
		ADDR_OF_NONNULL(&module_ectx->counter_storage);

	struct counter_storage *rules_storage = module_ectx_counter_storage(
		module_ectx, acl_config->rules_registry_idx
	);

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

		// Classify each v6 address half once on the union tries. The
		// keys are packed per half so the batched walk can interleave
		// each trie's dependent page chains across the batch instead of
		// stalling on every hop of every packet.
		uint8_t src_hi_keys[count][8];
		uint8_t src_lo_keys[count][8];
		uint8_t dst_hi_keys[count][8];
		uint8_t dst_lo_keys[count][8];

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

			memcpy(src_hi_keys[idx], saddr, 8);
			memcpy(src_lo_keys[idx], saddr + 8, 8);
			memcpy(dst_hi_keys[idx], daddr, 8);
			memcpy(dst_lo_keys[idx], daddr + 8, 8);
		}

		uint32_t src_hi[count];
		uint32_t src_lo[count];
		uint32_t dst_hi[count];
		uint32_t dst_lo[count];

		lpm8_lookup_batch(
			&share_src->hi, src_hi_keys[0], src_hi, ip6_idx
		);
		lpm8_lookup_batch(
			&share_src->lo, src_lo_keys[0], src_lo, ip6_idx
		);
		lpm8_lookup_batch(
			&share_dst->hi, dst_hi_keys[0], dst_hi, ip6_idx
		);
		lpm8_lookup_batch(
			&share_dst->lo, dst_lo_keys[0], dst_lo, ip6_idx
		);

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

		if (packet->network_header.type ==
		    rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4)) {
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
						counter_get_address(
							target->counter_id,
							rules_storage
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
					if (fwstate_check_state(
						    fw4state,
						    fw6state,
						    packet,
						    now,
						    &push_sync_packet
					    )) {
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

			if (push_sync_packet != SYNC_NONE &&
			    fwstate_sync_emit_config_usable(sync_config)) {
				create_cnt[0] += 1;

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

				sync_cnt[0] += 1;
				sync_cnt[1] += packet_data_len(sync_pkt);
				packet_front_output(packet_front, sync_pkt);
			}
		} else {
			uint64_t *c = counter_get_address(
				acl_config->no_match_counter_id, counter_storage
			);
			c[0] += 1;

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
