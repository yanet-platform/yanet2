#pragma once

/*
 * Module authored attribute lookups of the mirror classifiers.
 *
 * Each leaf routine reads one attribute classifier the compile side
 * produced and yields the class of every packet of the batch; the
 * network getters are batched so the parsing loops amortize over the
 * batch. The dispatchers below pair the leaves with the joints of the
 * classifier structs exactly as the compile stages of
 * modules/mirror/api/controlplane.c joined them.
 */

#include <rte_ip.h>
#include <rte_mbuf.h>
#include <rte_memcpy.h>
#include <stdint.h>

#include "common/network.h"
#include "common/value.h"

#include "lib/classify/classifiers/device.h"
#include "lib/classify/classifiers/line.h"
#include "lib/classify/classifiers/net4.h"
#include "lib/classify/classifiers/net6.h"
#include "lib/classify/classify.h"

#include "lib/classify/query.h"

#include "lib/dataplane/packet/data.h"
#include "lib/dataplane/packet/packet.h"

#include "config.h"

static inline void
mirror_lookup_device(
	const struct classify_attr_device *attr,
	const uint64_t *cm_index,
	const struct packet **packets,
	uint32_t *results,
	uint32_t count
) {
	for (uint32_t idx = 0; idx < count; ++idx) {
		// The packet device resolves through the per generation
		// global-to-module mapping of the module execution context;
		// a device outside the module keeps the module device zero.
		uint32_t device_id = cm_index[packets[idx]->tx_device_id];
		if (device_id >= attr->line.size) {
			device_id = 0;
		}
		results[idx] =
			vline_get((struct vline *)&attr->line, device_id);
	}
}

static inline void
mirror_lookup_vlan(
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
mirror_packet_get_net4_src_batch(
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
mirror_packet_get_net4_dst_batch(
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
mirror_packet_get_net6_src_batch(
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
mirror_packet_get_net6_dst_batch(
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
 * Inline dispatchers of the dataplane hot path. Every leaf routine and
 * every library core is called by name, so the compiler inlines the
 * leaves with their packet getters into the dispatcher body and no per
 * attribute indirect dispatch remains. Every projection classification
 * is self contained: the joints and the decoder live in the projection
 * classifier itself.
 */

// The dispatcher scratch is one fixed frame per classifier: batches
// are processed in chunks of MIRROR_CLASSIFY_MAX_BATCH packets, and
// the worst frame - the ip6 dispatcher, four class arrays beside its
// 4KB address scratch with the joint stages chained in place through
// the class arrays - stays at 8KB, the size of the single values
// frame of the former tape walkers.
#define MIRROR_CLASSIFY_MAX_BATCH 256

static inline void
mirror_classify_vlan(
	const struct mirror_classifier_vlan *cls,
	const uint64_t *cm_index,
	const struct packet **packets,
	uint32_t *results,
	uint32_t packet_count
) {
	uint32_t dev[MIRROR_CLASSIFY_MAX_BATCH];
	uint32_t vlan[MIRROR_CLASSIFY_MAX_BATCH];

	for (uint32_t off = 0; off < packet_count;
	     off += MIRROR_CLASSIFY_MAX_BATCH) {
		uint32_t count = packet_count - off < MIRROR_CLASSIFY_MAX_BATCH
					 ? packet_count - off
					 : MIRROR_CLASSIFY_MAX_BATCH;
		const struct packet **batch = packets + off;

		mirror_lookup_device(
			&cls->dev_attr, cm_index, batch, dev, count
		);
		mirror_lookup_vlan(&cls->vlan_attr, batch, vlan, count);

		// The joint chains in place through the device array, so the
		// joined classes need no frame of their own.
		classify_joint_lookup(&cls->joint, dev, vlan, dev, count);
		classify_resolve(&cls->rule_map, dev, results + off, count);
	}
}

static inline void
mirror_classify_ip4(
	const struct mirror_classifier_ip4 *cls,
	const uint64_t *cm_index,
	const struct packet **packets,
	uint32_t *results,
	uint32_t packet_count
) {
	uint32_t dev[MIRROR_CLASSIFY_MAX_BATCH];
	uint32_t vlan[MIRROR_CLASSIFY_MAX_BATCH];
	uint32_t n4s[MIRROR_CLASSIFY_MAX_BATCH];
	uint32_t n4d[MIRROR_CLASSIFY_MAX_BATCH];
	uint32_t addrs4[MIRROR_CLASSIFY_MAX_BATCH];

	for (uint32_t off = 0; off < packet_count;
	     off += MIRROR_CLASSIFY_MAX_BATCH) {
		uint32_t count = packet_count - off < MIRROR_CLASSIFY_MAX_BATCH
					 ? packet_count - off
					 : MIRROR_CLASSIFY_MAX_BATCH;
		const struct packet **batch = packets + off;

		mirror_lookup_device(
			&cls->dev_attr, cm_index, batch, dev, count
		);
		mirror_lookup_vlan(&cls->vlan_attr, batch, vlan, count);

		mirror_packet_get_net4_src_batch(batch, addrs4, count);
		classify_net4_lookup(&cls->net4_src_attr, addrs4, n4s, count);

		mirror_packet_get_net4_dst_batch(batch, addrs4, count);
		classify_net4_lookup(&cls->net4_dst_attr, addrs4, n4d, count);

		// The joint stages chain in place through the class arrays:
		// every join consumes the partial classes of its two sides
		// and leaves its own in the array of its left side, so the
		// three stages need no frame of their own.
		classify_joint_lookup(
			&cls->dev_vlan_joint, dev, vlan, dev, count
		);
		classify_joint_lookup(&cls->nets_joint, n4s, n4d, n4s, count);
		classify_joint_lookup(&cls->root_joint, dev, n4s, dev, count);
		classify_resolve(&cls->rule_map, dev, results + off, count);
	}
}

static inline void
mirror_classify_ip6(
	const struct mirror_classifier_ip6 *cls,
	const uint64_t *cm_index,
	const struct packet **packets,
	uint32_t *results,
	uint32_t packet_count
) {
	uint32_t dev[MIRROR_CLASSIFY_MAX_BATCH];
	uint32_t vlan[MIRROR_CLASSIFY_MAX_BATCH];
	uint32_t n6s[MIRROR_CLASSIFY_MAX_BATCH];
	uint32_t n6d[MIRROR_CLASSIFY_MAX_BATCH];
	uint8_t addrs6[MIRROR_CLASSIFY_MAX_BATCH][NET6_LEN];

	for (uint32_t off = 0; off < packet_count;
	     off += MIRROR_CLASSIFY_MAX_BATCH) {
		uint32_t count = packet_count - off < MIRROR_CLASSIFY_MAX_BATCH
					 ? packet_count - off
					 : MIRROR_CLASSIFY_MAX_BATCH;
		const struct packet **batch = packets + off;

		mirror_lookup_device(
			&cls->dev_attr, cm_index, batch, dev, count
		);
		mirror_lookup_vlan(&cls->vlan_attr, batch, vlan, count);

		// One address scratch serves both sides: the lookup of a side
		// finishes before the getter of the other one rewrites it.
		mirror_packet_get_net6_src_batch(batch, addrs6[0], count);
		classify_net6_lookup(
			&cls->net6_src_attr, addrs6[0], n6s, count
		);

		mirror_packet_get_net6_dst_batch(batch, addrs6[0], count);
		classify_net6_lookup(
			&cls->net6_dst_attr, addrs6[0], n6d, count
		);

		// The joint stages chain in place through the class arrays:
		// every join consumes the partial classes of its two sides
		// and leaves its own in the array of its left side, so the
		// three stages need no frame of their own.
		classify_joint_lookup(
			&cls->dev_vlan_joint, dev, vlan, dev, count
		);
		classify_joint_lookup(&cls->nets_joint, n6s, n6d, n6s, count);
		classify_joint_lookup(&cls->root_joint, dev, n6s, dev, count);
		classify_resolve(&cls->rule_map, dev, results + off, count);
	}
}
