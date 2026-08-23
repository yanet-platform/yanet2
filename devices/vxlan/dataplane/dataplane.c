#include "dataplane.h"

#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include <rte_ether.h>
#include <rte_ip.h>
#include <rte_udp.h>
#include <rte_vxlan.h>

#include "config.h"

#include "common/container_of.h"

#include "lib/dataplane/module/packet_front.h"

#include "lib/dataplane/device/device.h"

#include "lib/dataplane/packet/data.h"
#include "lib/dataplane/packet/packet.h"
#include "lib/dataplane/pipeline/econtext.h"

// Outer headers a vxlan device prepends on output: Ethernet (14) +
// IPv4 (20) + UDP (8) + VXLAN (8).
#define VXLAN_ENCAP_LEN                                                        \
	(sizeof(struct rte_ether_hdr) + sizeof(struct rte_ipv4_hdr) +          \
	 sizeof(struct rte_udp_hdr) + sizeof(struct rte_vxlan_hdr))

// The outer UDP source port spreads flows over ECMP paths. It is derived
// from the inner packet hash inside the reserved ephemeral half of the
// port space, as recommended by RFC 7348 section 4.1.
#define VXLAN_SRC_PORT_BASE 49152
#define VXLAN_SRC_PORT_RANGE 16384

#define VXLAN_FLAGS_BYTE 0x08
#define VXLAN_OUTER_TTL 64

/*
 * Terminate a vxlan tunnel: strip the outer headers and re-parse the
 * inner Ethernet frame.
 *
 * A packet is decapsulated only when the outer Ethernet carries IPv4
 * directly, the IPv4 datagram is unfragmented, the declared IPv4 and
 * UDP length envelopes cover the VXLAN header, the UDP destination
 * port equals the configured port, and the VXLAN header carries the
 * I flag and the configured VNI. Trailing bytes past the declared UDP
 * length are trimmed, so the inner frame ends at the UDP payload end.
 * Anything else — including a different VNI or port — is dropped: the
 * device claims this port and VNI as its tunnel endpoint, so there is
 * no other device the packet could belong to.
 */
static void
vxlan_input_handle(
	struct dp_worker *dp_worker,
	struct device_ectx *device_ectx,
	struct packet_front *packet_front
) {
	(void)dp_worker;

	struct cp_device_vxlan *config = container_of(
		ADDR_OF(&device_ectx->cp_device),
		struct cp_device_vxlan,
		cp_device
	);

	struct packet *packet;
	while ((packet = packet_list_pop(&packet_front->input)) != NULL) {
		struct rte_mbuf *mbuf = packet_to_mbuf(packet);
		// The outer headers must sit contiguously in the head segment,
		// so their read bounds are checked against its length; the
		// declared-length envelopes below are checked against the
		// whole-packet length.
		uint16_t data_len = rte_pktmbuf_data_len(mbuf);

		if (data_len < sizeof(struct rte_ether_hdr)) {
			packet_front_drop(packet_front, packet);
			continue;
		}

		struct rte_ether_hdr *ether_hdr =
			rte_pktmbuf_mtod(mbuf, struct rte_ether_hdr *);
		if (ether_hdr->ether_type !=
		    rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4)) {
			packet_front_drop(packet_front, packet);
			continue;
		}

		uint16_t offset = sizeof(struct rte_ether_hdr);
		if (data_len < offset + sizeof(struct rte_ipv4_hdr)) {
			packet_front_drop(packet_front, packet);
			continue;
		}

		struct rte_ipv4_hdr *ipv4_hdr = rte_pktmbuf_mtod_offset(
			mbuf, struct rte_ipv4_hdr *, offset
		);
		uint8_t version = ipv4_hdr->version_ihl >> 4;
		uint8_t ihl = ipv4_hdr->version_ihl & RTE_IPV4_HDR_IHL_MASK;
		if (version != 4 || ihl < 5 ||
		    ipv4_hdr->next_proto_id != IPPROTO_UDP) {
			packet_front_drop(packet_front, packet);
			continue;
		}

		// A fragment must not be decapsulated: a non-first fragment's
		// payload can mimic the UDP and VXLAN headers, and an MF-set
		// first fragment ends mid-tunnel-packet.
		if (ipv4_hdr->fragment_offset & rte_cpu_to_be_16(0x3FFF)) {
			packet_front_drop(packet_front, packet);
			continue;
		}

		// The declared IPv4 length must cover its own header and stay
		// inside the frame; everything past it is padding.
		uint32_t total_length =
			rte_be_to_cpu_16(ipv4_hdr->total_length);
		uint32_t ip_end = offset + total_length;
		if (total_length < (uint32_t)ihl * RTE_IPV4_IHL_MULTIPLIER ||
		    ip_end > rte_pktmbuf_pkt_len(mbuf)) {
			packet_front_drop(packet_front, packet);
			continue;
		}

		offset += ihl * RTE_IPV4_IHL_MULTIPLIER;
		if (data_len < offset + sizeof(struct rte_udp_hdr)) {
			packet_front_drop(packet_front, packet);
			continue;
		}

		struct rte_udp_hdr *udp_hdr = rte_pktmbuf_mtod_offset(
			mbuf, struct rte_udp_hdr *, offset
		);
		if (udp_hdr->dst_port != rte_cpu_to_be_16(config->dst_port)) {
			packet_front_drop(packet_front, packet);
			continue;
		}

		// The declared UDP length must reach past the VXLAN header and
		// stay inside the IPv4 datagram; the datagram may still end
		// before the IPv4 envelope does.
		uint32_t dgram_len = rte_be_to_cpu_16(udp_hdr->dgram_len);
		uint32_t udp_end = offset + dgram_len;
		if (dgram_len < sizeof(struct rte_udp_hdr) +
					sizeof(struct rte_vxlan_hdr) ||
		    udp_end > ip_end) {
			packet_front_drop(packet_front, packet);
			continue;
		}

		offset += sizeof(struct rte_udp_hdr);
		if (data_len < offset + sizeof(struct rte_vxlan_hdr)) {
			packet_front_drop(packet_front, packet);
			continue;
		}

		struct rte_vxlan_hdr *vxlan_hdr = rte_pktmbuf_mtod_offset(
			mbuf, struct rte_vxlan_hdr *, offset
		);
		if (vxlan_hdr->flags != VXLAN_FLAGS_BYTE ||
		    rte_be_to_cpu_32(vxlan_hdr->vx_vni) >> 8 != config->vni) {
			packet_front_drop(packet_front, packet);
			continue;
		}

		offset += sizeof(struct rte_vxlan_hdr);

		// parse_packet bounds its header reads by the whole-packet
		// length but reads through the head segment, so the inner
		// Ethernet header must stay resident there after the adj — the
		// same residency every RX packet arrives with.
		if (data_len < offset + sizeof(struct rte_ether_hdr)) {
			packet_front_drop(packet_front, packet);
			continue;
		}

		// Cut the frame at the declared UDP end so the inner frame
		// ends exactly at the UDP payload end; the envelope checks
		// above guarantee udp_end <= ip_end <= pkt_len.
		// rte_pktmbuf_trim only removes bytes inside the last
		// segment, so a padding tail it cannot fully remove —
		// spanning segments or beyond 16 bits — would leave the
		// envelope unenforced and is dropped.
		uint32_t excess = rte_pktmbuf_pkt_len(mbuf) - udp_end;
		if (excess > 0 &&
		    (excess > UINT16_MAX ||
		     rte_pktmbuf_trim(mbuf, (uint16_t)excess) != 0)) {
			packet_front_drop(packet_front, packet);
			continue;
		}

		rte_pktmbuf_adj(mbuf, offset);
		packet_refresh_data_len(packet);

		// Re-run the standard parse so downstream pipelines see the
		// inner frame's headers, hash and length; a malformed inner
		// frame is dropped. parse_packet never overwrites the vlan id
		// and the transport header of a frame that carries neither (an
		// untagged non-IP inner frame), so reset both to the
		// fresh-packet state — a stripped outer VLAN tag and the outer
		// UDP header must not leak into the inner pipelines' filters.
		packet->vlan = 0;
		packet->transport_header.type = PACKET_HEADER_TYPE_UNKNOWN;
		packet->transport_header.offset = 0;
		if (parse_packet(packet)) {
			packet_front_drop(packet_front, packet);
			continue;
		}

		packet_front_output(packet_front, packet);
	}
}

/*
 * Encapsulate packets routed to this device: prepend outer Ethernet,
 * IPv4, UDP and VXLAN headers built from the shared-memory config.
 *
 * The outer IPv4 header sets DF so the tunnel never relies on underlay
 * fragmentation, and carries a computed header checksum. The UDP checksum
 * stays zero, which is legal over IPv4. Packets too large for the 16-bit
 * IPv4 total length, or without headroom for the outer headers, are
 * dropped.
 */
static void
vxlan_output_handle(
	struct dp_worker *dp_worker,
	struct device_ectx *device_ectx,
	struct packet_front *packet_front
) {
	(void)dp_worker;

	struct cp_device_vxlan *config = container_of(
		ADDR_OF(&device_ectx->cp_device),
		struct cp_device_vxlan,
		cp_device
	);

	struct packet *packet;
	while ((packet = packet_list_pop(&packet_front->input)) != NULL) {
		struct rte_mbuf *mbuf = packet_to_mbuf(packet);

		// The outer total_length = pkt_len + 36 (IPv4 + UDP + VXLAN; it
		// excludes the outer Ethernet header) must fit 16 bits, and DF
		// forbids fragmentation, so a larger frame cannot be
		// encapsulated at all.
		if (rte_pktmbuf_pkt_len(mbuf) >
		    UINT16_MAX -
			    (VXLAN_ENCAP_LEN - sizeof(struct rte_ether_hdr))) {
			packet_front_drop(packet_front, packet);
			continue;
		}

		// The prepend lands in the head segment, whose data_len is its
		// own 16-bit field: a single-segment frame the total-length
		// check accepts could still wrap it, and rte_pktmbuf_prepend
		// guards headroom only, not the enlarged head length.
		if (rte_pktmbuf_data_len(mbuf) > UINT16_MAX - VXLAN_ENCAP_LEN) {
			packet_front_drop(packet_front, packet);
			continue;
		}

		struct rte_ether_hdr *ether_hdr = (struct rte_ether_hdr *)
			rte_pktmbuf_prepend(mbuf, VXLAN_ENCAP_LEN);
		if (ether_hdr == NULL) {
			packet_front_drop(packet_front, packet);
			continue;
		}
		packet_refresh_data_len(packet);

		/*
		 * Rebase the parse metadata onto the outer headers the way a
		 * re-parse would compute them — the device's output pipelines
		 * consume this metadata — except the hash: it stays
		 * inner-derived so the pipeline demux keeps the flow on one
		 * worker, and the UDP source port below already spreads flows
		 * over ECMP paths. The outer header is unfragmented, so the
		 * fragment state clears.
		 */
		packet->network_header.type =
			rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4);
		packet->network_header.offset = sizeof(struct rte_ether_hdr);
		packet->transport_header.type = IPPROTO_UDP;
		packet->transport_header.offset = sizeof(struct rte_ether_hdr) +
						  sizeof(struct rte_ipv4_hdr);
		packet->flags = 0;
		packet->fragment_offset = 0;
		// vlan is reset even though parse_packet would not clear it:
		// RX packets start zeroed, so the parser never manifests the
		// untagged state, but an inner VLAN tag leaves its id here
		// while the outer frame the pipelines see is untagged.
		packet->vlan = 0;

		uint16_t outer_len = (uint16_t)rte_pktmbuf_pkt_len(mbuf);

		memcpy(&ether_hdr->dst_addr,
		       config->dst_mac,
		       sizeof(ether_hdr->dst_addr));
		memcpy(&ether_hdr->src_addr,
		       config->src_mac,
		       sizeof(ether_hdr->src_addr));
		ether_hdr->ether_type = rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4);

		struct rte_ipv4_hdr *ipv4_hdr =
			(struct rte_ipv4_hdr *)(ether_hdr + 1);
		ipv4_hdr->version_ihl = (4 << 4) | (sizeof(*ipv4_hdr) /
						    RTE_IPV4_IHL_MULTIPLIER);
		ipv4_hdr->type_of_service = 0;
		ipv4_hdr->total_length = rte_cpu_to_be_16(
			outer_len - sizeof(struct rte_ether_hdr)
		);
		ipv4_hdr->packet_id = 0;
		ipv4_hdr->fragment_offset =
			rte_cpu_to_be_16(RTE_IPV4_HDR_DF_FLAG);
		ipv4_hdr->time_to_live = VXLAN_OUTER_TTL;
		ipv4_hdr->next_proto_id = IPPROTO_UDP;
		ipv4_hdr->hdr_checksum = 0;
		ipv4_hdr->src_addr = config->src_ip;
		ipv4_hdr->dst_addr = config->dst_ip;
		ipv4_hdr->hdr_checksum = rte_ipv4_cksum(ipv4_hdr);

		struct rte_udp_hdr *udp_hdr =
			(struct rte_udp_hdr *)(ipv4_hdr + 1);
		udp_hdr->src_port = rte_cpu_to_be_16(
			VXLAN_SRC_PORT_BASE +
			packet->hash % VXLAN_SRC_PORT_RANGE
		);
		udp_hdr->dst_port = rte_cpu_to_be_16(config->dst_port);
		udp_hdr->dgram_len = rte_cpu_to_be_16(
			outer_len - sizeof(struct rte_ether_hdr) -
			sizeof(struct rte_ipv4_hdr)
		);
		udp_hdr->dgram_cksum = 0;

		struct rte_vxlan_hdr *vxlan_hdr =
			(struct rte_vxlan_hdr *)(udp_hdr + 1);
		vxlan_hdr->vx_flags = RTE_BE32(VXLAN_FLAGS_BYTE << 24);
		vxlan_hdr->vx_vni = rte_cpu_to_be_32(config->vni << 8);

		packet_front_output(packet_front, packet);
	}
}

struct device_vxlan {
	struct device device;
};

struct device *
new_device_vxlan() {
	struct device_vxlan *device_vxlan =
		(struct device_vxlan *)malloc(sizeof(struct device_vxlan));

	if (device_vxlan == NULL) {
		return NULL;
	}

	snprintf(
		device_vxlan->device.name,
		sizeof(device_vxlan->device.name),
		"%s",
		"vxlan"
	);
	device_vxlan->device.input_handler = vxlan_input_handle;
	device_vxlan->device.output_handler = vxlan_output_handle;

	return &device_vxlan->device;
}
