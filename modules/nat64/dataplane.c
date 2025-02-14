#include "dataplane.h"

#include <string.h>

#include <netinet/icmp6.h>
#include <netinet/ip_icmp.h>

#include <rte_ether.h>
#include <rte_ip.h>
#include <rte_log.h>
#include <rte_mbuf.h>
#include <rte_memcpy.h>
#include <rte_tcp.h>
#include <rte_udp.h>

#include <rte_icmp.h>
#include <rte_jhash.h>
#include <rte_mbuf.h>

#include "dataplane/module/module.h"

#define RTE_LOGTYPE_NAT64 RTE_LOGTYPE_USER1

void
add_ip4to6(struct ip4to6 **hash, uint32_t *ip4, uint8_t *ip6) {
	struct ip4to6 *s;

	s = malloc(sizeof *s);
	s->ip4 = ip4;
	s->ip6 = ip6;
	HASH_ADD_KEYPTR(hh, *hash, ip4, sizeof(uint32_t), s);
}

void
add_ip6to4(struct ip4to6 **hash, uint8_t *ip6, uint32_t *ip4) {
	struct ip4to6 *s;

	s = malloc(sizeof *s);
	s->ip4 = ip4;
	s->ip6 = ip6;
	HASH_ADD_KEYPTR(hh, *hash, ip6, 16 * sizeof(uint8_t), s);
	char ip_str[2 * INET6_ADDRSTRLEN];
	inet_ntop(AF_INET6, ip6, ip_str, INET6_ADDRSTRLEN);
	inet_ntop(AF_INET, ip4, ip_str + INET6_ADDRSTRLEN + 1, INET_ADDRSTRLEN);
	RTE_LOG(INFO,
		NAT64,
		"Adding mapping %s -> %s\n",
		ip_str,
		ip_str + INET6_ADDRSTRLEN + 1);
}

struct ip4to6 *
find_ip6to4(struct ip4to6 **hash, uint8_t *ip6) {
	struct ip4to6 *s = NULL;

	HASH_FIND(hh, *hash, ip6, 16 * sizeof(uint8_t), s);
	return s;
}

struct ip4to6 *
find_ip4to6(struct ip4to6 **hash, uint32_t *ip4) {
	struct ip4to6 *s = NULL;

	HASH_FIND(hh, *hash, ip4, sizeof(uint32_t), s);
	return s;
}

static int
nat64_handle_configure(
	struct module *module,
	const void *config_data,
	size_t config_data_size,
	struct module_config **new_config
) {
	(void)module;
	(void)config_data;
	(void)config_data_size;
	(void)new_config;

	struct nat64_module_config *config = (struct nat64_module_config *)
		malloc(sizeof(struct nat64_module_config));

	config->hash4to6 = NULL;
	config->hash6to4 = NULL;

	// FIXME: handle errors
	// lpm_init(&config->map4to6);
	// lpm_init(&config->map6to4);

	// FIXME: deserialization
	uintptr_t pos = (uintptr_t)config_data;
	uintptr_t end = pos + config_data_size;
	// FIXME: check data boundaries
	(void)end;

	uint32_t mapping_count = *(uint32_t *)pos;
	pos += sizeof(mapping_count);
	RTE_LOG(INFO, NAT64, "mapping count %d\n", mapping_count);
	while (mapping_count--) {
		uint32_t *addr4 = (uint32_t *)pos;
		pos += 4;
		uint8_t *addr6 = (uint8_t *)pos;
		pos += 16;
		add_ip4to6(&config->hash4to6, addr4, addr6);
		add_ip6to4(&config->hash6to4, addr6, addr4);
		// lpm_insert(&config->map4to6, 4, from, from, addr6);
	}

	*new_config = &config->config;
	RTE_LOG(INFO, NAT64, "NAT64 module configured\n");

	return 0;
}

// TODO: Errors reporting
static inline int
icmp_v6_to_v4(
	/* in */ struct icmp6_hdr *icmpHeader
) {

	uint8_t type = icmpHeader->icmp6_type;
	uint8_t code = icmpHeader->icmp6_code;

	RTE_LOG(INFO, NAT64, "ICMPv6 type: %d, code: %d \n", type, code);

	switch (type) {
	case ICMP6_DST_UNREACH:
		type = ICMP_UNREACH;

		switch (code) {
		case ICMP6_DST_UNREACH_NOROUTE:
		case ICMP6_DST_UNREACH_BEYONDSCOPE:
		case ICMP6_DST_UNREACH_ADDR:
			code = ICMP_HOST_UNREACH;
			break;
		case ICMP6_DST_UNREACH_ADMIN:
			code = ICMP_HOST_ANO;
			break;
		case ICMP6_DST_UNREACH_NOPORT:
			code = ICMP_PORT_UNREACH;
			break;

		default:
			return -1;
		}

		break;

	case ICMP6_PACKET_TOO_BIG:
		type = ICMP_DEST_UNREACH;
		code = ICMP_FRAG_NEEDED;

		uint32_t mtu = rte_be_to_cpu_32(icmpHeader->icmp6_mtu) - 20;
		icmpHeader->icmp6_mtu = 0;
		icmpHeader->icmp6_data16[1] = rte_cpu_to_be_16(mtu);
		break;

	case ICMP6_TIME_EXCEEDED:
		type = ICMP_TIME_EXCEEDED;
		break;

	case ICMP6_PARAM_PROB:

		switch (code) {
		case ICMP6_PARAMPROB_HEADER:
			type = ICMP_PARAMPROB;
			code = 0;
			// TODO: calc
			break;
		case ICMP6_PARAMPROB_NEXTHEADER:
			type = ICMP_DEST_UNREACH;
			code = ICMP_PROT_UNREACH;
			icmpHeader->icmp6_pptr = 0;
			break;

		default:
			return -1;
			break;
		}

		break;

	default:
		break;
	}

	RTE_LOG(INFO, NAT64, "translate ICMP type: %d, code: %d \n", type, code
	);

	icmpHeader->icmp6_type = type;
	icmpHeader->icmp6_code = code;

	return 0;
}

static int
nat64_handle_v6(struct ip4to6 **hash, struct packet *packet) {
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);

	struct rte_ipv6_hdr *ipv6Header = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_ipv6_hdr *, packet->network_header.offset
	);

	if (ipv6Header->proto == IPPROTO_FRAGMENT) {
		RTE_LOG(INFO, NAT64, "drop fragment\n");
		return -1; // TODO: handle fragmented packets
	}

	// TODO: do we need to check ttl?

	char ip_str[2 * INET6_ADDRSTRLEN];
	inet_ntop(AF_INET6, &ipv6Header->src_addr, ip_str, INET6_ADDRSTRLEN);
	struct ip4to6 *new_src_addr =
		find_ip6to4(hash, (uint8_t *)&ipv6Header->src_addr);
	if (NULL == new_src_addr) {
		RTE_LOG(INFO, NAT64, "not found mapping for %s. Drop\n", ip_str
		);
		// Если не найдено соответствующего IPv4-адреса, дропаем пакет
		return -1;
	}

	inet_ntop(
		AF_INET,
		new_src_addr->ip4,
		ip_str + INET6_ADDRSTRLEN + 1,
		INET_ADDRSTRLEN
	);
	RTE_LOG(INFO,
		NAT64,
		"Found mapping %s -> %s\n",
		ip_str,
		ip_str + INET6_ADDRSTRLEN + 1);

	// calculate delta for new packet length
	uint16_t delta = packet->transport_header.offset -
			 packet->network_header.offset -
			 sizeof(struct rte_ipv4_hdr);
	RTE_LOG(INFO,
		NAT64,
		"transport header offset: %d, network: %d, si: %lu, delta: "
		"%d\n",
		packet->transport_header.offset,
		packet->network_header.offset,
		sizeof(struct rte_ipv4_hdr),
		delta);

	struct rte_ipv4_hdr *new_ipv4_header = rte_pktmbuf_mtod_offset(
		mbuf,
		struct rte_ipv4_hdr *,
		packet->network_header.offset + delta
	);

	uint16_t payload_length = rte_be_to_cpu_16(ipv6Header->payload_len);

	new_ipv4_header->version_ihl = RTE_IPV4_VHL_DEF;
	new_ipv4_header->type_of_service =
		(rte_be_to_cpu_32(ipv6Header->vtc_flow) >> 20) & 0xFF;
	new_ipv4_header->total_length =
		rte_cpu_to_be_16(payload_length + sizeof(struct rte_ipv4_hdr));

	new_ipv4_header->packet_id = 0;	      // TODO: generate id
	new_ipv4_header->fragment_offset = 0; // TODO: handle fragmentation
	new_ipv4_header->time_to_live =
		ipv6Header->hop_limits; // TODO: decrement ttl?
	new_ipv4_header->next_proto_id = ipv6Header->proto;
	new_ipv4_header->hdr_checksum = 0;

	memcpy(&new_ipv4_header->src_addr, new_src_addr->ip4, sizeof(uint32_t));
	// memcpy(&new_ipv4_header->dst_addr, new_dst_addr->ip4, sizeof(struct
	// in_addr));

	// handle ICMP, TCP, UDP
	if (ipv6Header->proto == IPPROTO_ICMPV6) {
		new_ipv4_header->next_proto_id = IPPROTO_ICMP;

		struct icmp6_hdr *icmpHeader = rte_pktmbuf_mtod_offset(
			mbuf,
			struct icmp6_hdr *,
			packet->transport_header.offset
		);
		int result = icmp_v6_to_v4(icmpHeader);
		if (result) {
			RTE_LOG(ERR, NAT64, "icmp_v6_to_v4 failed\n");
			return result;
		}
	}
	// copy l2 header
	rte_memcpy(
		rte_pktmbuf_mtod_offset(mbuf, char *, delta),
		rte_pktmbuf_mtod(mbuf, char *),
		packet->network_header.offset
	);

	// reduce packet
	if (rte_pktmbuf_adj(mbuf, delta) == NULL) {
		RTE_LOG(ERR, NAT64, "adjust mbuf failed. Delta: %d\n", delta);
		return -1;
	}

	// adjust new transport header offset
	packet->transport_header.offset =
		packet->network_header.offset + sizeof(struct rte_ipv4_hdr);

	// set ipv4 header type
	uint16_t *next_header_type = rte_pktmbuf_mtod_offset(
		mbuf, uint16_t *, packet->network_header.offset - 2
	);
	*next_header_type = rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4);

	// Обновляем длину пакета в структуре mbuf
	mbuf->pkt_len -= 20;
	mbuf->data_len -= 20;

	return 0;
}

static int
nat64_handle_v4(struct ip4to6 **hash, struct packet *packet) {

	struct rte_mbuf *mbuf = packet_to_mbuf(packet);

	struct rte_ipv4_hdr *ipv4Header = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_ipv4_hdr *, packet->network_header.offset
	);

	uint32_t new_packet_len =
		mbuf->pkt_len + sizeof(struct in6_addr) - sizeof(uint32_t);

	uint16_t delta =
		packet->transport_header.offset -
		packet->network_header.offset +
		(sizeof(struct rte_ipv6_hdr) - sizeof(struct rte_ipv4_hdr));

	rte_pktmbuf_prepend(
		mbuf, sizeof(struct rte_ipv6_hdr) - sizeof(struct rte_ipv4_hdr)
	);
	struct rte_ipv6_hdr *new_ipv6_header = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_ipv6_hdr *, packet->network_header.offset
	);

	rte_memcpy(
		rte_pktmbuf_mtod(mbuf, char *),
		rte_pktmbuf_mtod_offset(mbuf, char *, delta),
		packet->network_header.offset
	);

	new_ipv6_header->vtc_flow = ipv4Header->type_of_service;
	new_ipv6_header->payload_len = rte_cpu_to_be_16(new_packet_len);
	new_ipv6_header->hop_limits =
		ipv4Header->time_to_live; // TODO: decrement?
	new_ipv6_header->proto = ipv4Header->next_proto_id;

	uint32_t addr4 = rte_be_to_cpu_32(ipv4Header->dst_addr);
	struct ip4to6 *entry = find_ip4to6(hash, &addr4);
	if (!entry) {
		return -1; // Не найдено соответствующего IPv6-адреса
	}

	memcpy(&new_ipv6_header->dst_addr, entry->ip6, 16 * sizeof(uint8_t));

	return 0;
}

static void
nat64_handle_packets(
	struct module *module,
	struct module_config *config,
	struct packet_front *packet_front
) {
	(void)module;
	struct nat64_module_config *nat64_config =
		container_of(config, struct nat64_module_config, config);

	struct packet *packet;
	while ((packet = packet_list_pop(&packet_front->input)) != NULL) {
		int result = 0;
		if (packet->network_header.type ==
		    rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4)) {
			result = nat64_handle_v4(
				&nat64_config->hash4to6, packet
			);
		} else if (packet->network_header.type ==
			   rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6)) {
			result = nat64_handle_v6(
				&nat64_config->hash6to4, packet
			);
		}

		if (result) {
			packet_front_drop(packet_front, packet);
		} else {
			packet_front_output(packet_front, packet);
		}
	}
}

struct module *
new_module_nat64() {
	struct nat64_module *module =
		(struct nat64_module *)malloc(sizeof(struct nat64_module));

	if (module == NULL) {
		return NULL;
	}

	snprintf(
		module->module.name, sizeof(module->module.name), "%s", "nat64"
	);
	module->module.handler = nat64_handle_packets;
	module->module.config_handler = nat64_handle_configure;

	return &module->module;
}