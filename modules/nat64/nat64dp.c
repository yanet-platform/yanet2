#include "nat64dp.h"

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

#include "dataplane/module/module.h"

#define RTE_LOGTYPE_NAT64 RTE_LOGTYPE_USER1

void
add_ip4to6(
	struct ip4to6 **hash, uint32_t *ip4, uint8_t *ip6, size_t prefix_index
) {
	struct ip4to6 *s;

	s = rte_malloc(sizeof *s);
	if (!s) {
		RTE_LOG(ERR,
			NAT64,
			"Failed to allocate memory for ip4to6 entry\n");
		return;
	}
	s->ip4 = ip4;
	s->ip6 = ip6;
	s->prefix_index = prefix_index;
	HASH_ADD_KEYPTR(hh, *hash, ip4, sizeof(uint32_t), s);
	LOG_DBG(NAT64,
		,
		"Adding mapping " IPv4_BYTES_FMT " -> " IPv6_BYTES_FMT
		", prefix#%zu\n",
		IPv4_BYTES(*ip4),
		IPv6_BYTES(ip6),
		prefix_index);
}

void
add_ip6to4(
	struct ip4to6 **hash, uint8_t *ip6, uint32_t *ip4, size_t prefix_index
) {
	struct ip4to6 *s;

	s = rte_malloc(sizeof *s);
	if (!s) {
		RTE_LOG(ERR,
			NAT64,
			"Failed to allocate memory for ip4to6 entry\n");
		return;
	}
	s->ip4 = ip4;
	s->ip6 = ip6;
	s->prefix_index = prefix_index;
	HASH_ADD_KEYPTR(hh, *hash, ip6, 16 * sizeof(uint8_t), s);
	LOG_DBG(NAT64,
		,
		"Adding mapping " IPv6_BYTES_FMT " -> " IPv4_BYTES_FMT
		", prefix#%zu\n",
		IPv6_BYTES(ip6),
		IPv4_BYTES(*ip4),
		prefix_index);
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
		rte_malloc(sizeof(struct nat64_module_config));

	if (!config) {
		RTE_LOG(ERR, NAT64, "Failed to allocate memory for config\n");
		return -1;
	}

	config->hash4to6 = NULL;
	config->hash6to4 = NULL;

	struct nat64_prefix prfxs[] = {
		[0] = {.prefix =
			       {0x2a,
				0x02,
				0x06,
				0xbc,
				0x00,
				0x00,
				0x00,
				0x00,
				0x00,
				0x00,
				0x00,
				0x00}}
	};

	config->ipv6_prefixes = prfxs;

	// FIXME: deserialization
	uintptr_t pos = (uintptr_t)config_data;
	uintptr_t end = pos + config_data_size;
	// FIXME: check data boundaries
	(void)end;

	uint32_t mapping_count = *(uint32_t *)pos;
	pos += sizeof(mapping_count);
	LOG_DBG(NAT64, , "mapping count %d\n", mapping_count);
	while (mapping_count--) {
		uint32_t *addr4 = (uint32_t *)pos;
		pos += 4;
		uint8_t *addr6 = (uint8_t *)pos;
		pos += 16;
		add_ip4to6(&config->hash4to6, addr4, addr6, 0);
		add_ip6to4(&config->hash6to4, addr6, addr4, 0);
	}

	*new_config = &config->config;
	LOG_DBG(NAT64, , "NAT64 module configured\n");

	return 0;
}

// TODO: Errors reporting
static inline int
icmp_v6_to_v4(
	/* in */ struct icmp6_hdr *icmpHeader
) {

	uint8_t type = icmpHeader->icmp6_type;
	uint8_t code = icmpHeader->icmp6_code;

	LOG_DBG(NAT64,
		,
		"start translate ICMPv6 type: %d, code: %d \n",
		type,
		code);

	switch (type) {
	case ICMP6_ECHO_REQUEST:
		type = ICMP_ECHO;
		break;
	case ICMP6_ECHO_REPLY:
		type = ICMP_ECHOREPLY;
		break;
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

	LOG_DBG(NAT64, , "translate ICMP type: %d, code: %d \n", type, code);

	icmpHeader->icmp6_type = type;
	icmpHeader->icmp6_code = code;

	return 0;
}

static int
nat64_handle_v6(
	struct nat64_module_config *nat64_config, struct packet *packet
) {
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);

	if (!mbuf) {
		RTE_LOG(ERR, NAT64, "Failed to get mbuf from packet\n");
		return -1;
	}

	struct rte_ipv6_hdr *ipv6Header = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_ipv6_hdr *, packet->network_header.offset
	);

	if (!ipv6Header) {
		RTE_LOG(ERR, NAT64, "Failed to get IPv6 header from mbuf\n");
		return -1;
	}

	if (ipv6Header->proto == IPPROTO_FRAGMENT) {
		LOG_DBG(NAT64, , "drop fragment\n");
		return -1; // TODO: handle fragmented packets
	}

	struct ip4to6 *new_src_addr = find_ip6to4(
		&nat64_config->hash6to4, (uint8_t *)&ipv6Header->src_addr
	);
	if (NULL == new_src_addr) {
		LOG_DBG(NAT64,
			,
			"not found mapping for " IPv6_BYTES_FMT ". Drop\n",
			IPv6_BYTES(ipv6Header->src_addr));
		return -1;
	}

	LOG_DBG(NAT64,
		,
		"found mapping " IPv6_BYTES_FMT " -> " IPv4_BYTES_FMT "\n",
		IPv6_BYTES(ipv6Header->src_addr),
		IPv4_BYTES(*new_src_addr->ip4));

	// calculate delta for new packet length
	uint16_t delta = packet->transport_header.offset -
			 packet->network_header.offset -
			 sizeof(struct rte_ipv4_hdr);
	LOG_DBG(NAT64,
		,
		"transport header offset: %d, network header offset: %d, "
		"delta: "
		"%d\n",
		packet->transport_header.offset,
		packet->network_header.offset,
		delta);

	struct rte_ipv4_hdr *new_ipv4_header = rte_pktmbuf_mtod_offset(
		mbuf,
		struct rte_ipv4_hdr *,
		packet->network_header.offset + delta
	);

	if (!new_ipv4_header) {
		RTE_LOG(ERR, NAT64, "Failed to get new IPv4 header from mbuf\n"
		);
		return -1;
	}

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

	rte_memcpy(&new_ipv4_header->src_addr, new_src_addr->ip4, sizeof(uint32_t));
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

		if (!icmpHeader) {
			RTE_LOG(ERR,
				NAT64,
				"Failed to get ICMPv6 header from mbuf\n");
			return -1;
		}

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
	if (!next_header_type) {
		RTE_LOG(ERR, NAT64, "Failed to get next header type from mbuf\n"
		);
		return -1;
	}

	*next_header_type = rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4);

	return 0;
}

static inline int
icmp_v4_to_v6(
	/* in */ struct icmp *icmpHeader
) {
	uint8_t type = icmpHeader->icmp_type;
	uint8_t code = icmpHeader->icmp_code;

	LOG_DBG(NAT64,
		,
		"start translation ICMPv4 type: %d, code: %d \n",
		type,
		code);

	switch (type) {
	case ICMP_UNREACH:
		type = ICMP6_DST_UNREACH;

		switch (code) {
		case ICMP_HOST_UNREACH:
			code = ICMP6_DST_UNREACH_ADDR;
			break;
		case ICMP_PORT_UNREACH:
			code = ICMP6_DST_UNREACH_NOPORT;
			break;
		case ICMP_NET_UNREACH:
			code = ICMP6_DST_UNREACH_ADMIN;
			break;
		case ICMP_FRAG_NEEDED:
			type = ICMP6_PACKET_TOO_BIG;
			code = 0;
			uint16_t mtu =
				rte_be_to_cpu_16(icmpHeader->icmp_nextmtu) + 20;
			icmpHeader->icmp_nextmtu = rte_cpu_to_be_32(mtu);
			break;

		default:
			return -1;
		}

		break;

	case ICMP_ECHO:
		type = ICMP6_ECHO_REQUEST; // Для ICMP_ECHO
		code = 0;
		break;
	case ICMP_ECHOREPLY:
		type = ICMP6_ECHO_REPLY; // Для ICMP_ECHO
		code = 0;
		break;

	case ICMP_TIME_EXCEEDED:
		type = ICMP6_TIME_EXCEEDED;
		break;

	case ICMP_PARAMPROB:
		type = ICMP6_PARAM_PROB;
		code = ICMP6_PARAMPROB_HEADER;
		break;

	default:
		RTE_LOG(WARNING,
			NAT64,
			"Unknown ICMPv4 type: %d, code: %d \n",
			type,
			code);
		// Возможна обработка неизвестных типов или возврат ошибки
		// return -1;
		break;
	}

	LOG_DBG(NAT64, , "translated ICMP type: %d, code: %d \n", type, code);

	icmpHeader->icmp_type = type;
	icmpHeader->icmp_code = code;

	return 0;
}

static int
nat64_handle_v4(
	struct nat64_module_config *nat64_config, struct packet *packet
) {

	struct rte_mbuf *mbuf = packet_to_mbuf(packet);

	if (!mbuf) {
		RTE_LOG(ERR, NAT64, "Failed to get mbuf from packet\n");
		return -1;
	}

	struct rte_ipv4_hdr *ipv4Header = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_ipv4_hdr *, packet->network_header.offset
	);

	if (!ipv4Header) {
		RTE_LOG(ERR, NAT64, "Failed to get IPv4 header from mbuf\n");
		return -1;
	}

	uint32_t addr4 = ipv4Header->dst_addr;
	struct ip4to6 *entry = find_ip4to6(&nat64_config->hash4to6, &addr4);
	if (!entry) {
		RTE_LOG(ERR,
			NAT64,
			"Failed to find IPv6 mapping for IPv4 address %x\n",
			addr4);
		return -1; // Не найдено соответствующего IPv6-адреса
	}

	int32_t delta =
		sizeof(struct rte_ipv6_hdr) - (packet->transport_header.offset -
					       packet->network_header.offset);

	char *nmbuf = NULL;
	if (delta >= 0) {
		nmbuf = rte_pktmbuf_prepend(mbuf, delta);
	} else {
		nmbuf = rte_pktmbuf_adj(mbuf, -delta);
	}
	if (!nmbuf) {
		RTE_LOG(ERR, NAT64, "Failed to prepend mbuf\n");
		return -1;
	}

	rte_memcpy(
		rte_pktmbuf_mtod(mbuf, char *),
		rte_pktmbuf_mtod_offset(mbuf, char *, delta),
		packet->network_header.offset
	);

	struct rte_ipv6_hdr *new_ipv6_header = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_ipv6_hdr *, packet->network_header.offset
	);

	if (!new_ipv6_header) {
		RTE_LOG(ERR, NAT64, "Failed to get new IPv6 header from mbuf\n"
		);
		return -1;
	}

	new_ipv6_header->vtc_flow = rte_cpu_to_be_32(
		(6 << 28) |
		(ipv4Header->type_of_service << RTE_IPV6_HDR_TC_SHIFT)
	);

	new_ipv6_header->payload_len = rte_cpu_to_be_16(
		rte_be_to_cpu_16(ipv4Header->total_length) -
		sizeof(struct rte_ipv4_hdr)
	);
	new_ipv6_header->hop_limits = ipv4Header->time_to_live;
	new_ipv6_header->proto = ipv4Header->next_proto_id;

	SET_IPV4_MAPPED_IPV6(
		&new_ipv6_header->src_addr,
		nat64_config->ipv6_prefixes[entry->prefix_index].prefix,
		&ipv4Header->src_addr
	);
	rte_memcpy(&new_ipv6_header->dst_addr, entry->ip6, 16 * sizeof(uint8_t));

	if (ipv4Header->next_proto_id == IPPROTO_ICMP) {
		new_ipv6_header->proto = IPPROTO_ICMPV6;
		int result = icmp_v4_to_v6(rte_pktmbuf_mtod_offset(
			mbuf, struct icmp *, packet->transport_header.offset
		));
		if (result) {
			RTE_LOG(ERR, NAT64, "icmp_v4_to_v6 failed\n");
			return result;
		}
	}

	packet->transport_header.offset += delta;

	struct rte_ether_hdr *ethHeader =
		rte_pktmbuf_mtod(mbuf, struct rte_ether_hdr *);
	if (!ethHeader) {
		RTE_LOG(ERR, NAT64, "Failed to get Ethernet header from mbuf\n"
		);
		return -1;
	}
	ethHeader->ether_type = rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6);

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
		int result = -1; // drop unknown ether type
		if (packet->network_header.type ==
		    rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4)) {
			result = nat64_handle_v4(nat64_config, packet);
		} else if (packet->network_header.type ==
			   rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6)) {
			result = nat64_handle_v6(nat64_config, packet);
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

#ifdef NAT64_DEBUG
	rte_log_set_level(RTE_LOGTYPE_NAT64, RTE_LOG_DEBUG);
#endif
	struct nat64_module *module =
		(struct nat64_module *)rte_malloc(sizeof(struct nat64_module));

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
