#pragma once

/*
 * Attribute lookups and batch classify routines of the classifier
 * object's bundles.
 *
 * The leaf routines read one attribute classifier of the compiled
 * bundle and yield the class of every packet of the batch; the network
 * and port getters are batched so the parsing loops amortize over the
 * batch. The two subtype routines below pair the leaves with the
 * joints of the bundle structs exactly as the compile stages of
 * objects/classifier/api/classifier_object.c joined them.
 *
 * Only classifier_object.c takes the subtype routines' addresses - the
 * handles the prepared buffers carry - so both stay static inline
 * here: a module reaches them through the public handler of
 * classifier_object.h alone.
 */

#include <string.h>

#include <rte_ether.h>
#include <rte_ip.h>
#include <rte_mbuf.h>
#include <rte_memcpy.h>
#include <stdint.h>

#include "common/network.h"
#include "common/value.h"

#include "lib/classify/classifiers/line.h"
#include "lib/classify/classifiers/net4.h"
#include "lib/classify/classifiers/net6.h"
#include "lib/classify/classify.h"

#include "lib/classify/query.h"

#include "lib/dataplane/packet/data.h"
#include "lib/dataplane/packet/packet.h"

#include "classifier_object.h"

// The classify scratch is one fixed frame per batch: batches are
// processed in chunks of CLS_CLASSIFY_MAX_BATCH packets, and the worst
// frame - the l3 routine, its class arrays beside the 4KB v6 address
// scratch - stays below the single values frames of the module
// dispatchers the routines were detached from.
#define CLS_CLASSIFY_MAX_BATCH 256

static inline void
cl_lookup_vlan(
	const struct classify_attr_line *attr,
	const struct packet **packets,
	uint32_t *results,
	uint32_t count
) {
	for (uint32_t idx = 0; idx < count; ++idx) {
		const uint16_t vlan_id = packets[idx]->vlan;
		results[idx] = vline_get((struct vline *)&attr->line, vlan_id);
	}
}

static inline void
cl_packet_get_net4_src_batch(
	const struct packet **packets, uint32_t *addrs, uint32_t packet_count
) {
	for (uint32_t idx = 0; idx < packet_count; ++idx) {
		const struct packet *packet = packets[idx];
		struct rte_mbuf *mbuf = packet_to_mbuf(packet);
		struct rte_ipv4_hdr *ipv4_hdr = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_ipv4_hdr *,
			packet->network_header.offset
		);
		// The header word can sit past the head segment of a chained
		// packet; the absent address classifies as zero.
		if (rte_pktmbuf_data_len(mbuf) >=
		    packet->network_header.offset + 4) {
			addrs[idx] = ipv4_hdr->src_addr;
		} else {
			addrs[idx] = 0;
		}
	}
}

static inline void
cl_packet_get_net4_dst_batch(
	const struct packet **packets, uint32_t *addrs, uint32_t packet_count
) {
	for (uint32_t idx = 0; idx < packet_count; ++idx) {
		const struct packet *packet = packets[idx];
		struct rte_mbuf *mbuf = packet_to_mbuf(packet);
		struct rte_ipv4_hdr *ipv4_hdr = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_ipv4_hdr *,
			packet->network_header.offset
		);
		// The header word can sit past the head segment of a chained
		// packet; the absent address classifies as zero.
		if (rte_pktmbuf_data_len(mbuf) >=
		    packet->network_header.offset + 4) {
			addrs[idx] = ipv4_hdr->dst_addr;
		} else {
			addrs[idx] = 0;
		}
	}
}

static inline void
cl_packet_get_net6_src_batch(
	const struct packet **packets, uint8_t *addrs, uint32_t packet_count
) {
	for (uint32_t idx = 0; idx < packet_count; ++idx) {
		const struct packet *packet = packets[idx];
		struct rte_mbuf *mbuf = packet_to_mbuf(packet);
		struct rte_ipv6_hdr *ipv6_hdr = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_ipv6_hdr *,
			packet->network_header.offset
		);
		// The address can sit past the head segment of a chained
		// packet; the absent address classifies as zero.
		if (rte_pktmbuf_data_len(mbuf) >=
		    packet->network_header.offset + NET6_LEN) {
			rte_mov16(addrs + idx * NET6_LEN, ipv6_hdr->src_addr);
		} else {
			memset(addrs + idx * NET6_LEN, 0, NET6_LEN);
		}
	}
}

static inline void
cl_packet_get_net6_dst_batch(
	const struct packet **packets, uint8_t *addrs, uint32_t packet_count
) {
	for (uint32_t idx = 0; idx < packet_count; ++idx) {
		const struct packet *packet = packets[idx];
		struct rte_mbuf *mbuf = packet_to_mbuf(packet);
		struct rte_ipv6_hdr *ipv6_hdr = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_ipv6_hdr *,
			packet->network_header.offset
		);
		// The address can sit past the head segment of a chained
		// packet; the absent address classifies as zero.
		if (rte_pktmbuf_data_len(mbuf) >=
		    packet->network_header.offset + NET6_LEN) {
			rte_mov16(addrs + idx * NET6_LEN, ipv6_hdr->dst_addr);
		} else {
			memset(addrs + idx * NET6_LEN, 0, NET6_LEN);
		}
	}
}

/*
 * Classifies one chunk through the shared core: the vlan classes of
 * the batch joined with the device class of the calling context, then
 * resolved through the l2 decoder. Returns the chunk's l2 results and
 * leaves the core classes in core_classes for the family joints.
 */
static inline void
cl_classify_core_chunk(
	const struct cl_classifier_core *core,
	const struct vline *l2_rule_map,
	uint32_t device_class,
	const struct packet **batch,
	uint32_t *core_classes,
	uint32_t *results,
	uint32_t count
) {
	uint32_t vlan[CLS_CLASSIFY_MAX_BATCH];

	cl_lookup_vlan(&core->vlan_attr, batch, vlan, count);

	classify_joint_lookup_const(
		&core->joint, device_class, vlan, core_classes, count
	);

	classify_resolve(l2_rule_map, core_classes, results, count);
}

/*
 * The l2 subtype routine: the whole bundle is the shared core, so
 * every packet resolves through the core classes and the l2 decoder.
 */
static inline void
classifier_object_classify_l2(
	const struct classifier_object *object,
	uint32_t device_class,
	const struct packet **packets,
	uint32_t *results,
	uint32_t count
) {
	uint32_t core_classes[CLS_CLASSIFY_MAX_BATCH];

	for (uint32_t off = 0; off < count; off += CLS_CLASSIFY_MAX_BATCH) {
		uint32_t chunk = count - off < CLS_CLASSIFY_MAX_BATCH
					 ? count - off
					 : CLS_CLASSIFY_MAX_BATCH;

		cl_classify_core_chunk(
			&object->core,
			&object->filter_l2.rule_map,
			device_class,
			packets + off,
			core_classes,
			results + off,
			chunk
		);
	}
}

/*
 * The l3 subtype routine: the l2 results of the shared core combined
 * with the family results of both network families, per packet by its
 * network ethertype, the least rule index of the applicable paths
 * winning - the first matching rule of the ruleset, in the order the
 * modules' own joined paths resolved it.
 */
static inline void
classifier_object_classify_l3(
	const struct classifier_object *object,
	uint32_t device_class,
	const struct packet **packets,
	uint32_t *results,
	uint32_t count
) {
	uint32_t core_classes[CLS_CLASSIFY_MAX_BATCH];

	const struct packet *ip4_packets[CLS_CLASSIFY_MAX_BATCH];
	uint32_t ip4_pos[CLS_CLASSIFY_MAX_BATCH];
	uint32_t core4_classes[CLS_CLASSIFY_MAX_BATCH];
	uint32_t net4_classes[CLS_CLASSIFY_MAX_BATCH];
	uint32_t ip4_results[CLS_CLASSIFY_MAX_BATCH];

	const struct packet *ip6_packets[CLS_CLASSIFY_MAX_BATCH];
	uint32_t ip6_pos[CLS_CLASSIFY_MAX_BATCH];
	uint32_t core6_classes[CLS_CLASSIFY_MAX_BATCH];
	uint32_t net6_classes[CLS_CLASSIFY_MAX_BATCH];
	uint32_t ip6_results[CLS_CLASSIFY_MAX_BATCH];

	uint32_t src[CLS_CLASSIFY_MAX_BATCH];
	uint32_t dst[CLS_CLASSIFY_MAX_BATCH];
	uint32_t addrs4[CLS_CLASSIFY_MAX_BATCH];
	uint8_t addrs6[CLS_CLASSIFY_MAX_BATCH][NET6_LEN];

	for (uint32_t off = 0; off < count; off += CLS_CLASSIFY_MAX_BATCH) {
		uint32_t chunk = count - off < CLS_CLASSIFY_MAX_BATCH
					 ? count - off
					 : CLS_CLASSIFY_MAX_BATCH;
		const struct packet **batch = packets + off;
		uint32_t *batch_results = results + off;

		cl_classify_core_chunk(
			&object->core,
			&object->filter_l2.rule_map,
			device_class,
			batch,
			core_classes,
			batch_results,
			chunk
		);

		// The family split of the chunk: the positions carry the
		// core classes of the family packets into the family
		// sub-batches.
		uint32_t ip4_idx = 0;
		uint32_t ip6_idx = 0;
		for (uint32_t idx = 0; idx < chunk; ++idx) {
			if (batch[idx]->network_header.type ==
			    rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4)) {
				ip4_pos[ip4_idx] = idx;
				ip4_packets[ip4_idx++] = batch[idx];
			}

			if (batch[idx]->network_header.type ==
			    rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6)) {
				ip6_pos[ip6_idx] = idx;
				ip6_packets[ip6_idx++] = batch[idx];
			}
		}

		if (ip4_idx != 0) {
			cl_packet_get_net4_src_batch(
				ip4_packets, addrs4, ip4_idx
			);
			classify_net4_lookup(
				&object->net4.src_attr, addrs4, src, ip4_idx
			);

			cl_packet_get_net4_dst_batch(
				ip4_packets, addrs4, ip4_idx
			);
			classify_net4_lookup(
				&object->net4.dst_attr, addrs4, dst, ip4_idx
			);

			classify_joint_lookup(
				&object->net4.joint,
				src,
				dst,
				net4_classes,
				ip4_idx
			);

			// The family root joins the core classes of the
			// family packets with the network pair classes and
			// resolves the joint classes through the family
			// decoder; the positions scatter the sub-batch
			// results back onto the packets, the least rule
			// index of the applicable paths winning.
			for (uint32_t idx = 0; idx < ip4_idx; ++idx) {
				core4_classes[idx] = core_classes[ip4_pos[idx]];
			}
			classify_combine(
				&object->filter_ip4.root_joint,
				&object->filter_ip4.rule_map,
				core4_classes,
				net4_classes,
				ip4_results,
				ip4_idx
			);
			for (uint32_t idx = 0; idx < ip4_idx; ++idx) {
				if (ip4_results[idx] <
				    batch_results[ip4_pos[idx]]) {
					batch_results[ip4_pos[idx]] =
						ip4_results[idx];
				}
			}
		}

		if (ip6_idx != 0) {
			cl_packet_get_net6_src_batch(
				ip6_packets, addrs6[0], ip6_idx
			);
			classify_net6_lookup(
				&object->net6.src_attr, addrs6[0], src, ip6_idx
			);

			cl_packet_get_net6_dst_batch(
				ip6_packets, addrs6[0], ip6_idx
			);
			classify_net6_lookup(
				&object->net6.dst_attr, addrs6[0], dst, ip6_idx
			);

			classify_joint_lookup(
				&object->net6.joint,
				src,
				dst,
				net6_classes,
				ip6_idx
			);

			for (uint32_t idx = 0; idx < ip6_idx; ++idx) {
				core6_classes[idx] = core_classes[ip6_pos[idx]];
			}
			classify_combine(
				&object->filter_ip6.root_joint,
				&object->filter_ip6.rule_map,
				core6_classes,
				net6_classes,
				ip6_results,
				ip6_idx
			);
			for (uint32_t idx = 0; idx < ip6_idx; ++idx) {
				if (ip6_results[idx] <
				    batch_results[ip6_pos[idx]]) {
					batch_results[ip6_pos[idx]] =
						ip6_results[idx];
				}
			}
		}
	}
}
