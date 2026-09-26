#pragma once

/*
 * Module authored attribute lookups of the acl classifiers.
 *
 * Each leaf routine reads one attribute classifier the compile side
 * produced and yields the class of every packet of the batch: the
 * packet getters are called directly, so their parsing is inlined into
 * the lookup body, and the network and port getters are batched so the
 * parsing loops amortize over the batch. The dispatchers below pair
 * the leaves with the joints of the classifier structs exactly as the
 * compile stages of modules/acl/api/controlplane.c joined them.
 */

#include <netinet/in.h>
#include <rte_icmp.h>
#include <rte_ip.h>
#include <rte_mbuf.h>
#include <rte_memcpy.h>
#include <rte_tcp.h>
#include <rte_udp.h>
#include <stdint.h>

#include "common/network.h"
#include "common/value.h"

#include "lib/classify/classifiers/device.h"
#include "lib/classify/classifiers/ipfrag.h"
#include "lib/classify/classifiers/line.h"
#include "lib/classify/classifiers/net4.h"
#include "lib/classify/classifiers/net6.h"
#include "lib/classify/classifiers/port.h"

#include "lib/classify/query.h"

#include "lib/dataplane/packet/data.h"
#include "lib/dataplane/packet/packet.h"

#include "config.h"

// The dispatcher scratch is one fixed frame per classifier: batches
// are processed in chunks of ACL_CLASSIFY_MAX_BATCH packets, and the
// worst frame - the v6 core dispatcher, four class arrays beside its
// 4KB address scratch with the joint stages chained in place through
// the class arrays - stays at 8KB, below the 12KB single values frame
// of the former tape walkers.
#define ACL_CLASSIFY_MAX_BATCH 256

static inline void
acl_lookup_device(
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
acl_lookup_ipfrag(
	const struct classify_attr_ipfrag *attr,
	const struct packet **packets,
	uint32_t *results,
	uint32_t count
) {
	for (uint32_t idx = 0; idx < count; ++idx) {
		const uint32_t id = packets[idx]->fragment_offset > 0 ? 1 : 0;
		results[idx] = value_table_get(&attr->value_table, 0, id);
	}
}

// The IP protocol number of the packet, read out of the IP header by
// the parser: present for every IP packet, later fragments included.
//
// A non-initial fragment carries the protocol with the
// header-unavailable tag set in the upper bits - the low byte keeps
// the declared protocol, so protocol-only rules keep matching the
// fragment - and the tag is masked away here. The protocol paths
// never see a tagged type: their batches take offset zero packets
// only, the transport header of which the parser guarantees present.
static inline void
acl_lookup_ipproto(
	const struct classify_attr_line *attr,
	const struct packet **packets,
	uint32_t *results,
	uint32_t count
) {
	for (uint32_t idx = 0; idx < count; ++idx) {
		results[idx] = vline_get(
			(struct vline *)&attr->line,
			packets[idx]->transport_header.type & 0xff
		);
	}
}

/*
 * The TCP flags byte of the packet. The path selection guarantees the
 * packets of the batch carry a TCP transport header at offset zero -
 * the flags byte sits after the ports and the sequence and
 * acknowledgment numbers.
 *
 * The parser validates the transport header against the whole packet
 * length, so a chained packet can carry the byte past the head
 * segment; the read below stays bounded by the bytes actually
 * classified.
 */
static inline void
acl_lookup_tcp_flags(
	const struct classify_attr_line *attr,
	const struct packet **packets,
	uint32_t *results,
	uint32_t count
) {
	for (uint32_t idx = 0; idx < count; ++idx) {
		const struct packet *packet = packets[idx];
		struct rte_mbuf *mbuf = packet_to_mbuf(packet);
		uint32_t flags = 0;

		if (rte_pktmbuf_data_len(mbuf) >=
		    packet->transport_header.offset + 14) {
			struct rte_tcp_hdr *tcp_hdr = rte_pktmbuf_mtod_offset(
				mbuf,
				struct rte_tcp_hdr *,
				packet->transport_header.offset
			);
			flags = tcp_hdr->tcp_flags;
		}

		results[idx] = vline_get((struct vline *)&attr->line, flags);
	}
}

/*
 * The leading type byte of an ICMP message - ICMP and ICMPv6 share
 * the byte semantics. The path selection guarantees the packets of
 * the batch carry the message at offset zero; a bare echo header
 * carries the type byte without the rest of the full struct.
 */
static inline void
acl_lookup_icmp_type(
	const struct classify_attr_line *attr,
	const struct packet **packets,
	uint32_t *results,
	uint32_t count
) {
	for (uint32_t idx = 0; idx < count; ++idx) {
		const struct packet *packet = packets[idx];
		struct rte_mbuf *mbuf = packet_to_mbuf(packet);
		uint32_t type = 0;

		if (rte_pktmbuf_data_len(mbuf) >
		    packet->transport_header.offset) {
			type = *((uint8_t *)rte_pktmbuf_mtod_offset(
				mbuf, uint8_t *, packet->transport_header.offset
			));
		}

		results[idx] = vline_get((struct vline *)&attr->line, type);
	}
}

static inline void
acl_packet_get_net4_src_batch(
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
acl_packet_get_net4_dst_batch(
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
acl_packet_get_net6_src_batch(
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
acl_packet_get_net6_dst_batch(
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

// The port pair occupies the first four bytes of the TCP and the UDP
// segment, so one word read and one byteswap cover both attributes of
// a packet; the rest of the header can sit in a later segment of a
// chained packet.
static inline void
acl_packet_get_ports_batch(
	const struct packet **packets,
	uint16_t *src_ports,
	uint16_t *dst_ports,
	uint32_t packet_count
) {
	for (uint32_t idx = 0; idx < packet_count; ++idx) {
		const struct packet *packet = packets[idx];
		struct rte_mbuf *mbuf = packet_to_mbuf(packet);
		uint32_t ports = 0;

		if (packet->transport_header.type == IPPROTO_TCP ||
		    packet->transport_header.type == IPPROTO_UDP) {
			if (rte_pktmbuf_data_len(mbuf) >=
			    packet->transport_header.offset + 4) {
				uint32_t raw;
				memcpy(&raw,
				       rte_pktmbuf_mtod_offset(
					       mbuf,
					       const void *,
					       packet->transport_header.offset
				       ),
				       sizeof(raw));
				ports = rte_be_to_cpu_32(raw);
			}
		}

		src_ports[idx] = ports >> 16;
		dst_ports[idx] = ports;
	}
}

// Classifies both port attributes of the ports classifier in one pass:
// one getter walk over the batch, then one line lookup per attribute
// and packet.
static inline void
acl_lookup_ports(
	const struct classify_attr_port *src_attr,
	const struct classify_attr_port *dst_attr,
	const struct packet **batch,
	uint32_t *src_results,
	uint32_t *dst_results,
	uint32_t packet_count
) {
	uint16_t src_ports[ACL_CLASSIFY_MAX_BATCH];
	uint16_t dst_ports[ACL_CLASSIFY_MAX_BATCH];
	acl_packet_get_ports_batch(batch, src_ports, dst_ports, packet_count);
	for (uint32_t idx = 0; idx < packet_count; ++idx) {
		src_results[idx] = vline_get(
			(struct vline *)&src_attr->line, src_ports[idx]
		);
		dst_results[idx] = vline_get(
			(struct vline *)&dst_attr->line, dst_ports[idx]
		);
	}
}

/*
 * Inline dispatchers of the dataplane hot path. Every leaf routine and
 * every library core is called by name, so the compiler inlines the
 * leaves with their packet getters into the dispatcher body and no per
 * attribute indirect dispatch remains.
 *
 * The family results share the core classification: the core
 * dispatcher evaluates the four family attributes once per family
 * batch, and the fragment and ports suffix dispatchers are combined
 * with the core classes through the family root joints at the caller.
 *
 * The l2 dispatcher covers every packet of the burst: a single device
 * attribute holds the whole classification of the rules without
 * networks, so the device classes resolve through the decoder directly
 * with no joint in between.
 */
static inline void
acl_classify_l2(
	const struct acl_classifier_l2 *cls,
	const uint64_t *cm_index,
	const struct vline *rule_map,
	const struct packet **packets,
	uint32_t *results,
	uint32_t packet_count
) {
	uint32_t dev[ACL_CLASSIFY_MAX_BATCH];

	for (uint32_t off = 0; off < packet_count;
	     off += ACL_CLASSIFY_MAX_BATCH) {
		uint32_t count = packet_count - off < ACL_CLASSIFY_MAX_BATCH
					 ? packet_count - off
					 : ACL_CLASSIFY_MAX_BATCH;

		acl_lookup_device(
			&cls->dev_attr, cm_index, packets + off, dev, count
		);
		classify_resolve(rule_map, dev, results + off, count);
	}
}

static inline void
acl_classify_core4(
	const struct acl_classifier_core4 *cls,
	const uint64_t *cm_index,
	const struct packet **packets,
	uint32_t *classes,
	uint32_t packet_count
) {
	uint32_t dev[ACL_CLASSIFY_MAX_BATCH];
	uint32_t n4s[ACL_CLASSIFY_MAX_BATCH];
	uint32_t n4d[ACL_CLASSIFY_MAX_BATCH];
	uint32_t ipproto[ACL_CLASSIFY_MAX_BATCH];
	uint32_t addrs4[ACL_CLASSIFY_MAX_BATCH];

	for (uint32_t off = 0; off < packet_count;
	     off += ACL_CLASSIFY_MAX_BATCH) {
		uint32_t count = packet_count - off < ACL_CLASSIFY_MAX_BATCH
					 ? packet_count - off
					 : ACL_CLASSIFY_MAX_BATCH;
		const struct packet **batch = packets + off;

		acl_lookup_device(&cls->dev_attr, cm_index, batch, dev, count);

		acl_packet_get_net4_src_batch(batch, addrs4, count);
		classify_net4_lookup(&cls->net4_src_attr, addrs4, n4s, count);

		acl_packet_get_net4_dst_batch(batch, addrs4, count);
		classify_net4_lookup(&cls->net4_dst_attr, addrs4, n4d, count);

		acl_lookup_ipproto(&cls->ipproto_attr, batch, ipproto, count);

		// The joint stages chain in place through the class arrays:
		// every join consumes the partial classes of its two sides
		// and leaves its own in the array of its left side, so the
		// stages need no frame of their own - the final one resolves
		// straight into the caller's classes.
		classify_joint_lookup(&cls->nets_joint, n4s, n4d, n4s, count);
		classify_joint_lookup(&cls->mid_joint, dev, n4s, dev, count);
		classify_joint_lookup(
			&cls->proto_joint, dev, ipproto, classes + off, count
		);
	}
}

static inline void
acl_classify_core6(
	const struct acl_classifier_core6 *cls,
	const uint64_t *cm_index,
	const struct packet **packets,
	uint32_t *classes,
	uint32_t packet_count
) {
	uint32_t dev[ACL_CLASSIFY_MAX_BATCH];
	uint32_t n6s[ACL_CLASSIFY_MAX_BATCH];
	uint32_t n6d[ACL_CLASSIFY_MAX_BATCH];
	uint32_t ipproto[ACL_CLASSIFY_MAX_BATCH];
	uint8_t addrs6[ACL_CLASSIFY_MAX_BATCH][NET6_LEN];

	for (uint32_t off = 0; off < packet_count;
	     off += ACL_CLASSIFY_MAX_BATCH) {
		uint32_t count = packet_count - off < ACL_CLASSIFY_MAX_BATCH
					 ? packet_count - off
					 : ACL_CLASSIFY_MAX_BATCH;
		const struct packet **batch = packets + off;

		acl_lookup_device(&cls->dev_attr, cm_index, batch, dev, count);

		// One address scratch serves both sides: the lookup of a side
		// finishes before the getter of the other one rewrites it.
		acl_packet_get_net6_src_batch(batch, addrs6[0], count);
		classify_net6_lookup(
			&cls->net6_src_attr, addrs6[0], n6s, count
		);

		acl_packet_get_net6_dst_batch(batch, addrs6[0], count);
		classify_net6_lookup(
			&cls->net6_dst_attr, addrs6[0], n6d, count
		);

		acl_lookup_ipproto(&cls->ipproto_attr, batch, ipproto, count);

		// The joint stages chain in place through the class arrays:
		// every join consumes the partial classes of its two sides
		// and leaves its own in the array of its left side, so the
		// stages need no frame of their own - the final one resolves
		// straight into the caller's classes.
		classify_joint_lookup(&cls->nets_joint, n6s, n6d, n6s, count);
		classify_joint_lookup(&cls->mid_joint, dev, n6s, dev, count);
		classify_joint_lookup(
			&cls->proto_joint, dev, ipproto, classes + off, count
		);
	}
}

static inline void
acl_classify_frag(
	const struct acl_classifier_frag *cls,
	const struct packet **packets,
	uint32_t *classes,
	uint32_t packet_count
) {
	for (uint32_t off = 0; off < packet_count;
	     off += ACL_CLASSIFY_MAX_BATCH) {
		uint32_t count = packet_count - off < ACL_CLASSIFY_MAX_BATCH
					 ? packet_count - off
					 : ACL_CLASSIFY_MAX_BATCH;
		acl_lookup_ipfrag(
			&cls->frag_attr, packets + off, classes + off, count
		);
	}
}

static inline void
acl_classify_ports(
	const struct acl_classifier_ports *cls,
	const struct packet **packets,
	uint32_t *classes,
	uint32_t packet_count
) {
	uint32_t src[ACL_CLASSIFY_MAX_BATCH];
	uint32_t dst[ACL_CLASSIFY_MAX_BATCH];

	for (uint32_t off = 0; off < packet_count;
	     off += ACL_CLASSIFY_MAX_BATCH) {
		uint32_t count = packet_count - off < ACL_CLASSIFY_MAX_BATCH
					 ? packet_count - off
					 : ACL_CLASSIFY_MAX_BATCH;
		const struct packet **batch = packets + off;

		acl_lookup_ports(
			&cls->src_attr, &cls->dst_attr, batch, src, dst, count
		);
		classify_joint_lookup(
			&cls->joint, src, dst, classes + off, count
		);
	}
}

/*
 * The protocol path dispatchers of the dataplane hot path. Every leaf
 * routine is called by name over the shared core classes of the
 * family, so the whole path inlines into the handler body.
 *
 * The tcp path evaluates the shared ports pair and the flags leaf,
 * joins the flags classes onto the ports classes through the flags
 * joint and combines the result with the core classes through the
 * path root joint. The udp path combines the ports classes with the
 * core classes directly. The icmp path combines the type classes with
 * the core classes. The core classes arrive from the caller: the
 * family core classifier evaluated them once for the whole batch.
 */
static inline void
acl_classify_tcp4(
	const struct acl_classifier_ports *ports,
	const struct acl_classifier_tcp *tcp,
	const struct acl_filter_ip4_tcp *flt,
	const uint32_t *core_classes,
	const struct packet **packets,
	uint32_t *results,
	uint32_t packet_count
) {
	uint32_t src[ACL_CLASSIFY_MAX_BATCH];
	uint32_t dst[ACL_CLASSIFY_MAX_BATCH];
	uint32_t flags[ACL_CLASSIFY_MAX_BATCH];
	uint32_t mid[ACL_CLASSIFY_MAX_BATCH];

	for (uint32_t off = 0; off < packet_count;
	     off += ACL_CLASSIFY_MAX_BATCH) {
		uint32_t count = packet_count - off < ACL_CLASSIFY_MAX_BATCH
					 ? packet_count - off
					 : ACL_CLASSIFY_MAX_BATCH;
		const struct packet **batch = packets + off;

		acl_lookup_ports(
			&ports->src_attr,
			&ports->dst_attr,
			batch,
			src,
			dst,
			count
		);
		acl_lookup_tcp_flags(&tcp->flags_attr, batch, flags, count);

		classify_joint_lookup(&ports->joint, src, dst, src, count);
		classify_joint_lookup(
			&tcp->flags_joint, src, flags, mid, count
		);
		classify_combine(
			&flt->root_joint,
			&flt->rule_map,
			core_classes + off,
			mid,
			results + off,
			count
		);
	}
}

static inline void
acl_classify_udp4(
	const struct acl_classifier_ports *ports,
	const struct acl_filter_ip4_udp *flt,
	const uint32_t *core_classes,
	const struct packet **packets,
	uint32_t *results,
	uint32_t packet_count
) {
	uint32_t src[ACL_CLASSIFY_MAX_BATCH];
	uint32_t dst[ACL_CLASSIFY_MAX_BATCH];

	for (uint32_t off = 0; off < packet_count;
	     off += ACL_CLASSIFY_MAX_BATCH) {
		uint32_t count = packet_count - off < ACL_CLASSIFY_MAX_BATCH
					 ? packet_count - off
					 : ACL_CLASSIFY_MAX_BATCH;
		const struct packet **batch = packets + off;

		acl_lookup_ports(
			&ports->src_attr,
			&ports->dst_attr,
			batch,
			src,
			dst,
			count
		);

		classify_joint_lookup(&ports->joint, src, dst, src, count);
		classify_combine(
			&flt->root_joint,
			&flt->rule_map,
			core_classes + off,
			src,
			results + off,
			count
		);
	}
}

static inline void
acl_classify_icmp4(
	const struct acl_classifier_icmp *icmp,
	const struct acl_filter_ip4_icmp *flt,
	const uint32_t *core_classes,
	const struct packet **packets,
	uint32_t *results,
	uint32_t packet_count
) {
	uint32_t type[ACL_CLASSIFY_MAX_BATCH];

	for (uint32_t off = 0; off < packet_count;
	     off += ACL_CLASSIFY_MAX_BATCH) {
		uint32_t count = packet_count - off < ACL_CLASSIFY_MAX_BATCH
					 ? packet_count - off
					 : ACL_CLASSIFY_MAX_BATCH;
		const struct packet **batch = packets + off;

		acl_lookup_icmp_type(&icmp->type_attr, batch, type, count);
		classify_combine(
			&flt->root_joint,
			&flt->rule_map,
			core_classes + off,
			type,
			results + off,
			count
		);
	}
}

// The ip6 paths, in the same shape as the ip4 ones with the ip6
// filter types.
static inline void
acl_classify_tcp6(
	const struct acl_classifier_ports *ports,
	const struct acl_classifier_tcp *tcp,
	const struct acl_filter_ip6_tcp *flt,
	const uint32_t *core_classes,
	const struct packet **packets,
	uint32_t *results,
	uint32_t packet_count
) {
	uint32_t src[ACL_CLASSIFY_MAX_BATCH];
	uint32_t dst[ACL_CLASSIFY_MAX_BATCH];
	uint32_t flags[ACL_CLASSIFY_MAX_BATCH];
	uint32_t mid[ACL_CLASSIFY_MAX_BATCH];

	for (uint32_t off = 0; off < packet_count;
	     off += ACL_CLASSIFY_MAX_BATCH) {
		uint32_t count = packet_count - off < ACL_CLASSIFY_MAX_BATCH
					 ? packet_count - off
					 : ACL_CLASSIFY_MAX_BATCH;
		const struct packet **batch = packets + off;

		acl_lookup_ports(
			&ports->src_attr,
			&ports->dst_attr,
			batch,
			src,
			dst,
			count
		);
		acl_lookup_tcp_flags(&tcp->flags_attr, batch, flags, count);

		classify_joint_lookup(&ports->joint, src, dst, src, count);
		classify_joint_lookup(
			&tcp->flags_joint, src, flags, mid, count
		);
		classify_combine(
			&flt->root_joint,
			&flt->rule_map,
			core_classes + off,
			mid,
			results + off,
			count
		);
	}
}

static inline void
acl_classify_udp6(
	const struct acl_classifier_ports *ports,
	const struct acl_filter_ip6_udp *flt,
	const uint32_t *core_classes,
	const struct packet **packets,
	uint32_t *results,
	uint32_t packet_count
) {
	uint32_t src[ACL_CLASSIFY_MAX_BATCH];
	uint32_t dst[ACL_CLASSIFY_MAX_BATCH];

	for (uint32_t off = 0; off < packet_count;
	     off += ACL_CLASSIFY_MAX_BATCH) {
		uint32_t count = packet_count - off < ACL_CLASSIFY_MAX_BATCH
					 ? packet_count - off
					 : ACL_CLASSIFY_MAX_BATCH;
		const struct packet **batch = packets + off;

		acl_lookup_ports(
			&ports->src_attr,
			&ports->dst_attr,
			batch,
			src,
			dst,
			count
		);

		classify_joint_lookup(&ports->joint, src, dst, src, count);
		classify_combine(
			&flt->root_joint,
			&flt->rule_map,
			core_classes + off,
			src,
			results + off,
			count
		);
	}
}

static inline void
acl_classify_icmp6(
	const struct acl_classifier_icmp *icmp,
	const struct acl_filter_ip6_icmp *flt,
	const uint32_t *core_classes,
	const struct packet **packets,
	uint32_t *results,
	uint32_t packet_count
) {
	uint32_t type[ACL_CLASSIFY_MAX_BATCH];

	for (uint32_t off = 0; off < packet_count;
	     off += ACL_CLASSIFY_MAX_BATCH) {
		uint32_t count = packet_count - off < ACL_CLASSIFY_MAX_BATCH
					 ? packet_count - off
					 : ACL_CLASSIFY_MAX_BATCH;
		const struct packet **batch = packets + off;

		acl_lookup_icmp_type(&icmp->type_attr, batch, type, count);
		classify_combine(
			&flt->root_joint,
			&flt->rule_map,
			core_classes + off,
			type,
			results + off,
			count
		);
	}
}
