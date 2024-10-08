#include "classify.h"

#include "dataplane/packet/packet.h"

#include "rte_mbuf.h"
#include "rte_ether.h"
#include "rte_ip.h"
#include "rte_tcp.h"
#include "rte_udp.h"

#include "ipfw.h"

#include "lpm.h"



uint32_t
filter_classify_src_net_hi(
	const struct filter *filter,
	const struct packet *packet)
{
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);

	if (packet->network_header.type == rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4)) {
		struct rte_ipv4_hdr* ipv4Header = NULL;
		ipv4Header = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_ipv4_hdr*,
			packet->network_header.offset);

		(void) ipv4Header;
		return 0;
	}

	struct rte_ipv6_hdr* ipv6Header = NULL;
	ipv6Header = rte_pktmbuf_mtod_offset(
		mbuf,
		struct rte_ipv6_hdr*,
		packet->network_header.offset);

	const struct ipfw_packet_filter *ipfw_filter =
		(const struct ipfw_packet_filter *)filter;

	return lpm64_lookup(&ipfw_filter->src_net6_hi, *(uint64_t *)ipv6Header->src_addr);
}


uint32_t
filter_classify_src_net_lo(
	const struct filter *filter,
	const struct packet *packet)
{
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);

	if (packet->network_header.type == rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4)) {
		struct rte_ipv4_hdr* ipv4Header = NULL;
		ipv4Header = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_ipv4_hdr*,
			packet->network_header.offset);

		(void) ipv4Header;
		return 0;
	}

	struct rte_ipv6_hdr* ipv6Header = NULL;
	ipv6Header = rte_pktmbuf_mtod_offset(
		mbuf,
		struct rte_ipv6_hdr*,
		packet->network_header.offset);

	const struct ipfw_packet_filter *ipfw_filter =
		(const struct ipfw_packet_filter *)filter;

	return lpm64_lookup(&ipfw_filter->src_net6_hi, *(uint64_t *)(ipv6Header->src_addr + 8));

}

uint32_t
filter_classify_dst_net_hi(
	const struct filter *filter,
	const struct packet *packet)
{
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);

	if (packet->network_header.type == rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4)) {
		struct rte_ipv4_hdr* ipv4Header = NULL;
		ipv4Header = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_ipv4_hdr*,
			packet->network_header.offset);

		(void) ipv4Header;
		return 0;
	}

	struct rte_ipv6_hdr* ipv6Header = NULL;
	ipv6Header = rte_pktmbuf_mtod_offset(
		mbuf,
		struct rte_ipv6_hdr*,
		packet->network_header.offset);

	const struct ipfw_packet_filter *ipfw_filter =
		(const struct ipfw_packet_filter *)filter;

	return lpm64_lookup(&ipfw_filter->src_net6_hi, *(uint64_t *)ipv6Header->src_addr);
}


uint32_t
filter_classify_dst_net_lo(
	const struct filter *filter,
	const struct packet *packet)
{
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);

	if (packet->network_header.type == rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4)) {
		struct rte_ipv4_hdr* ipv4Header = NULL;
		ipv4Header = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_ipv4_hdr*,
			packet->network_header.offset);

		(void) ipv4Header;
		return 0;
	}

	struct rte_ipv6_hdr* ipv6Header = NULL;
	ipv6Header = rte_pktmbuf_mtod_offset(
		mbuf,
		struct rte_ipv6_hdr*,
		packet->network_header.offset);

	const struct ipfw_packet_filter *ipfw_filter =
		(const struct ipfw_packet_filter *)filter;

	return lpm64_lookup(&ipfw_filter->src_net6_hi, *(uint64_t *)(ipv6Header->dst_addr + 8));

}

uint32_t
filter_classify_src_port(
	const struct filter *filter,
	const struct packet *packet)
{
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);
	// TODO: what about protocols whithout port defined?
	uint16_t src_port = 0;

	if (packet->transport_header.type == IPPROTO_TCP) {
		struct rte_tcp_hdr* tcpHeader = NULL;
		tcpHeader = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_tcp_hdr*,
			packet->transport_header.offset);

		src_port = tcpHeader->src_port;
	} else if (packet->transport_header.type == IPPROTO_UDP) {
		struct rte_udp_hdr* udpHeader = NULL;
		udpHeader = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_udp_hdr*,
			packet->transport_header.offset);

		src_port = udpHeader->src_port;
	}

	const struct ipfw_packet_filter *ipfw_filter =
		(const struct ipfw_packet_filter *)filter;

	return ipfw_filter->src_port[src_port];
}

uint32_t
filter_classify_dst_port(
	const struct filter *filter,
	const struct packet *packet)
{
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);
	// TODO: what about protocols whithout port defined?
	uint16_t dst_port = 0;

	if (packet->transport_header.type == IPPROTO_TCP) {
		struct rte_tcp_hdr* tcpHeader = NULL;
		tcpHeader = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_tcp_hdr*,
			packet->transport_header.offset);

		dst_port = tcpHeader->dst_port;
	} else if (packet->transport_header.type == IPPROTO_UDP) {
		struct rte_udp_hdr* udpHeader = NULL;
		udpHeader = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_udp_hdr*,
			packet->transport_header.offset);

		dst_port = udpHeader->dst_port;
	}

	const struct ipfw_packet_filter *ipfw_filter =
		(const struct ipfw_packet_filter *)filter;

	return ipfw_filter->dst_port[dst_port];
}


