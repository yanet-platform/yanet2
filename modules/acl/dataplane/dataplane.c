#include "dataplane.h"
#include "config.h"

#include <rte_ether.h>

#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "controlplane/config/econtext.h"
#include "dataplane/module/module.h"
#include "dataplane/packet/packet.h"
#include "dataplane/time/clock.h"
#include "dataplane/worker.h"
#include "dataplane/worker/worker.h"
#include "fwstate/lookup.h"
#include "fwstate/sync.h"
#include "lib/dataplane/module/packet_front.h"
#include "logging/log.h"

#include <filter/query.h>

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

// Runs the filter_query classification pass with two leaf rows injected.
//
// Behaves exactly like filter_query, except that the leaf slot rows of the
// ext_src_lookup and ext_dst_lookup attributes are copied from the
// caller-supplied arrays instead of being computed by the per-filter leaf
// lookups.
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

static bool
acl_net6_share_verify_enabled(void) {
	static int state = -1;
	if (state < 0) {
		state = getenv("ACL_NET6_SHARE_VERIFY") != NULL;
	}
	return state != 0;
}

// Testing aid guarded by the ACL_NET6_SHARE_VERIFY environment variable.
//
// Recomputes one shared leaf row with the filter's own leaf lookup and
// aborts on any divergence from the remap-translated slots.
static void
acl_net6_share_verify(
	struct filter *filter,
	const struct filter_query *fq,
	size_t lookup,
	struct packet **packets,
	const uint32_t *slots,
	uint32_t count,
	const char *name
) {
	if (!acl_net6_share_verify_enabled()) {
		return;
	}

	uint32_t reference[count + 1];
	const struct filter_vertex *v = &filter->v[fq->lookup_count + lookup];
	fq->lookups[lookup](ADDR_OF(&v->data), packets, reference, count);
	if (memcmp(reference, slots, sizeof(uint32_t) * count) != 0) {
		LOG(ERROR,
		    "shared net6 slots diverge from %s leaf lookup %zu",
		    name,
		    lookup);
		abort();
	}
}

static bool
acl_net6_share_stats_enabled(void) {
	static int state = -1;
	if (state < 0) {
		state = getenv("ACL_NET6_SHARE_STATS") != NULL;
	}
	return state != 0;
}

// Row-constancy memo for the stats aid: 0 unknown, 1 constant, 2 varies.
//
// Keyed by comb table pointer; single-worker benchmark use only.
static uint8_t *
acl_share_stats_memo(const struct value_table *comb) {
	static const struct value_table *combs[8];
	static uint8_t *states[8];

	for (size_t idx = 0; idx < 8; ++idx) {
		if (combs[idx] == comb) {
			return states[idx];
		}
		if (combs[idx] == NULL) {
			combs[idx] = comb;
			states[idx] = calloc(comb->v_dim, 1);
			return states[idx];
		}
	}
	abort();
}

// True when every lo class maps the hi class to the same leaf class,
// i.e. the lo half of the address carries no information for this hi.
static bool
acl_share_stats_row_const(struct value_table *comb, uint32_t hi_class) {
	uint8_t *state = acl_share_stats_memo(comb);
	if (state[hi_class] == 0) {
		uint32_t first = value_table_get(comb, hi_class, 0);
		state[hi_class] = 1;
		for (uint32_t lo = 1; lo < comb->h_dim; ++lo) {
			if (value_table_get(comb, hi_class, lo) != first) {
				state[hi_class] = 2;
				break;
			}
		}
	}
	return state[hi_class] == 1;
}

// Testing aid guarded by the ACL_NET6_SHARE_STATS environment variable.
//
// Counts, over live traffic, how often the comb row of the packet's hi
// class is constant per filter and direction, and how often both
// filters agree so the union lo walk itself would be skippable.
static void
acl_net6_share_stats_account(
	struct value_table *comb_src_a,
	struct value_table *comb_dst_a,
	struct value_table *comb_src_b,
	struct value_table *comb_dst_b,
	const uint32_t *src_hi_a,
	const uint32_t *dst_hi_a,
	const uint32_t *src_hi_b,
	const uint32_t *dst_hi_b,
	const uint32_t *src_hi,
	const uint32_t *dst_hi,
	uint64_t ip6_idx,
	uint64_t ip6_port_idx,
	const uint32_t *ip6_port_pos
) {
	static uint64_t total, port_total;
	static uint64_t src_const_a, dst_const_a;
	static uint64_t src_const_b, dst_const_b;
	static uint64_t src_walk_skip, dst_walk_skip;

	bool in_port[ip6_idx + 1];
	bool src_ok_b[ip6_idx + 1];
	bool dst_ok_b[ip6_idx + 1];
	memset(in_port, 0, sizeof(in_port));

	for (uint64_t idx = 0; idx < ip6_port_idx; ++idx) {
		uint32_t pos = ip6_port_pos[idx];
		in_port[pos] = true;
		src_ok_b[pos] = acl_share_stats_row_const(
			comb_src_b, src_hi_b[src_hi[pos]]
		);
		dst_ok_b[pos] = acl_share_stats_row_const(
			comb_dst_b, dst_hi_b[dst_hi[pos]]
		);
	}

	for (uint64_t idx = 0; idx < ip6_idx; ++idx) {
		bool src_ok_a = acl_share_stats_row_const(
			comb_src_a, src_hi_a[src_hi[idx]]
		);
		bool dst_ok_a = acl_share_stats_row_const(
			comb_dst_a, dst_hi_a[dst_hi[idx]]
		);

		++total;
		src_const_a += src_ok_a;
		dst_const_a += dst_ok_a;
		if (in_port[idx]) {
			++port_total;
			src_const_b += src_ok_b[idx];
			dst_const_b += dst_ok_b[idx];
		}
		src_walk_skip += src_ok_a && (!in_port[idx] || src_ok_b[idx]);
		dst_walk_skip += dst_ok_a && (!in_port[idx] || dst_ok_b[idx]);
	}

	fprintf(stderr,
		"net6 share stats: v6=%lu port=%lu | src const ip6=%lu "
		"ip6_port=%lu walk_skip=%lu | dst const ip6=%lu ip6_port=%lu "
		"walk_skip=%lu\n",
		total,
		port_total,
		src_const_a,
		src_const_b,
		src_walk_skip,
		dst_const_a,
		dst_const_b,
		dst_walk_skip);
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
	const bool net6_share = acl_config->net6_share_enabled != 0;

	struct fwstate_config *fwstate_config = &acl_config->fwstate_cfg;
	struct fwstate_sync_config *sync_config = &fwstate_config->sync_config;
	fwmap_t *fw4state = ADDR_OF(&fwstate_config->fw4state);
	fwmap_t *fw6state = ADDR_OF(&fwstate_config->fw6state);
	fwmap_t *state_table = NULL;

	uint64_t *pass_cnt = counter_get_address(
		acl_config->action_allow_counter_id,
		dp_worker->idx,
		ADDR_OF(&module_ectx->counter_storage)
	);

	uint64_t *deny_cnt = counter_get_address(
		acl_config->action_deny_counter_id,
		dp_worker->idx,
		ADDR_OF(&module_ectx->counter_storage)
	);

	uint64_t *create_cnt = counter_get_address(
		acl_config->action_create_state_counter_id,
		dp_worker->idx,
		ADDR_OF(&module_ectx->counter_storage)
	);

	uint64_t *check_pass_cnt = counter_get_address(
		acl_config->action_check_pass_counter_id,
		dp_worker->idx,
		ADDR_OF(&module_ectx->counter_storage)
	);

	uint64_t *check_miss_cnt = counter_get_address(
		acl_config->action_check_miss_counter_id,
		dp_worker->idx,
		ADDR_OF(&module_ectx->counter_storage)
	);

	uint64_t *sync_cnt = counter_get_address(
		acl_config->sync_sent_counter_id,
		dp_worker->idx,
		ADDR_OF(&module_ectx->counter_storage)
	);

	uint64_t *invalid_cnt = counter_get_address(
		acl_config->action_invalid_counter_id,
		dp_worker->idx,
		ADDR_OF(&module_ectx->counter_storage)
	);

	uint64_t *non_term_cnt = counter_get_address(
		acl_config->action_non_term_counter_id,
		dp_worker->idx,
		ADDR_OF(&module_ectx->counter_storage)
	);

	// Time in nanoseconds is sufficient for keeping state up to 500 years
	uint64_t now = dp_worker->current_time;

	/*
	 * There are two major options:
	 *  - process packets one by one
	 *  - process stages one by one
	 * For the second option we have to split v4 and v6 processing.
	 */

	struct packet *vlan_packets[packet_list_count(&packet_front->input)];
	uint32_t vlan_result[packet_list_count(&packet_front->input)];
	uint64_t vlan_idx = 0;

	struct packet *ip4_packets[packet_list_count(&packet_front->input)];
	uint32_t ip4_result[packet_list_count(&packet_front->input)];
	uint64_t ip4_idx = 0;

	struct packet
		*ip4_port_packets[packet_list_count(&packet_front->input)];
	uint32_t ip4_port_result[packet_list_count(&packet_front->input)];
	uint64_t ip4_port_idx = 0;

	struct packet *ip6_packets[packet_list_count(&packet_front->input)];
	uint32_t ip6_result[packet_list_count(&packet_front->input)];
	uint64_t ip6_idx = 0;

	struct packet
		*ip6_port_packets[packet_list_count(&packet_front->input)];
	uint32_t ip6_port_result[packet_list_count(&packet_front->input)];
	uint64_t ip6_port_idx = 0;

	// Position of each ip6_port batch packet within the ip6 batch, filled
	// only on the shared-classification path where every ip6_port packet
	// is by construction also an ip6 packet.
	uint32_t ip6_port_pos[packet_list_count(&packet_front->input)];

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
		// tries are packed at compile time unless the allocator could
		// not serve the arenas, so the mutable walk stays a fallback.
		uint32_t src_hi[packet_list_count(&packet_front->input)];
		uint32_t src_lo[packet_list_count(&packet_front->input)];
		uint32_t dst_hi[packet_list_count(&packet_front->input)];
		uint32_t dst_lo[packet_list_count(&packet_front->input)];

		const uint32_t *src_hi_pages =
			ADDR_OF(&share_src->hi_packed.pages);
		const uint32_t *src_lo_pages =
			ADDR_OF(&share_src->lo_packed.pages);
		const uint32_t *dst_hi_pages =
			ADDR_OF(&share_dst->hi_packed.pages);
		const uint32_t *dst_lo_pages =
			ADDR_OF(&share_dst->lo_packed.pages);

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

			if (src_hi_pages != NULL) {
				src_hi[idx] =
					lpm8_packed_lookup(src_hi_pages, saddr);
				src_lo[idx] = lpm8_packed_lookup(
					src_lo_pages, saddr + 8
				);
			} else {
				src_hi[idx] =
					lpm8_lookup(&share_src->hi, saddr);
				src_lo[idx] =
					lpm8_lookup(&share_src->lo, saddr + 8);
			}

			if (dst_hi_pages != NULL) {
				dst_hi[idx] =
					lpm8_packed_lookup(dst_hi_pages, daddr);
				dst_lo[idx] = lpm8_packed_lookup(
					dst_lo_pages, daddr + 8
				);
			} else {
				dst_hi[idx] =
					lpm8_lookup(&share_dst->hi, daddr);
				dst_lo[idx] =
					lpm8_lookup(&share_dst->lo, daddr + 8);
			}
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
		struct net6_classifier *ip6_src_cls = (struct net6_classifier *)
			ADDR_OF(&acl_config->filter_ip6.v[8].data);
		struct net6_classifier *ip6_dst_cls = (struct net6_classifier *)
			ADDR_OF(&acl_config->filter_ip6.v[9].data);

		uint32_t ip6_src_slots[packet_list_count(&packet_front->input)];
		uint32_t ip6_dst_slots[packet_list_count(&packet_front->input)];

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

		struct net6_classifier *ip6_port_src_cls =
			(struct net6_classifier *)ADDR_OF(
				&acl_config->filter_ip6_port.v[9].data
			);
		struct net6_classifier *ip6_port_dst_cls =
			(struct net6_classifier *)ADDR_OF(
				&acl_config->filter_ip6_port.v[10].data
			);

		uint32_t ip6_port_src_slots[packet_list_count(
			&packet_front->input
		)];
		uint32_t ip6_port_dst_slots[packet_list_count(
			&packet_front->input
		)];

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

		if (acl_net6_share_stats_enabled() && ip6_idx > 0) {
			acl_net6_share_stats_account(
				&ip6_src_cls->comb,
				&ip6_dst_cls->comb,
				&ip6_port_src_cls->comb,
				&ip6_port_dst_cls->comb,
				src_hi_a,
				dst_hi_a,
				src_hi_b,
				dst_hi_b,
				src_hi,
				dst_hi,
				ip6_idx,
				ip6_port_idx,
				ip6_port_pos
			);
		}

		acl_net6_share_verify(
			&acl_config->filter_ip6,
			filter_ip6,
			2,
			ip6_packets,
			ip6_src_slots,
			ip6_idx,
			"filter_ip6"
		);
		acl_net6_share_verify(
			&acl_config->filter_ip6,
			filter_ip6,
			3,
			ip6_packets,
			ip6_dst_slots,
			ip6_idx,
			"filter_ip6"
		);
		acl_net6_share_verify(
			&acl_config->filter_ip6_port,
			filter_ip6_port,
			2,
			ip6_port_packets,
			ip6_port_src_slots,
			ip6_port_idx,
			"filter_ip6_port"
		);
		acl_net6_share_verify(
			&acl_config->filter_ip6_port,
			filter_ip6_port,
			3,
			ip6_port_packets,
			ip6_port_dst_slots,
			ip6_port_idx,
			"filter_ip6_port"
		);

		acl_filter_query_ext(
			&acl_config->filter_ip6,
			filter_ip6,
			ip6_packets,
			ip6_result,
			ip6_idx,
			2,
			ip6_src_slots,
			3,
			ip6_dst_slots
		);
		acl_filter_query_ext(
			&acl_config->filter_ip6_port,
			filter_ip6_port,
			ip6_port_packets,
			ip6_port_result,
			ip6_port_idx,
			2,
			ip6_port_src_slots,
			3,
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
			state_table = fw4state;

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
			state_table = fw6state;

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

		if (action != FILTER_RULE_INVALID)
			target = ADDR_OF(&acl_config->targets) + action;

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
					uint64_t *counters = counter_get_address(
						target->counter_id,
						dp_worker->idx,
						ADDR_OF(&module_ectx
								 ->counter_storage
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
					if (fwstate_check_state(
						    state_table,
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

			if (push_sync_packet != SYNC_NONE) {
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
				acl_config->no_match_counter_id,
				dp_worker->idx,
				ADDR_OF(&module_ectx->counter_storage)
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
