#include "dataplane.h"

#include <string.h>

#include <rte_ether.h>
#include <rte_ip.h>
#include <rte_mbuf.h>
#include <rte_ether.h>
#include <rte_tcp.h>
#include <rte_udp.h>

#include <rte_mbuf.h>
#include <rte_icmp.h>
#include <rte_jhash.h>

#include "dataplane/module/module.h"

void add_ip4to6(struct ip4to6 **hash, uint8_t* ip4, uint8_t* ip6) {
    struct ip4to6 *s;

    s = malloc(sizeof *s);
    s->ip4 = ip4;
    s->ip6 = ip6;
	HASH_ADD_KEYPTR(hh, *hash, ip4, 4 * sizeof(uint8_t), s);
}

void add_ip6to4(struct ip4to6 **hash, uint8_t* ip6, uint8_t* ip4) {
    struct ip4to6 *s;

    s = malloc(sizeof *s);
    s->ip4 = ip4;
    s->ip6 = ip6;
	HASH_ADD_KEYPTR(hh, *hash, ip6, 16 * sizeof(uint8_t), s);
}

struct ip4to6*
find_ip6to4(struct ip4to6 **hash, uint8_t* ip6) {
	struct ip4to6 *s = NULL;

	HASH_FIND(hh, *hash, ip6, 16 * sizeof(uint8_t), s);
	return s;
}

struct ip4to6*
find_ip4to6(struct ip4to6 **hash, uint8_t* ip4) {
	struct ip4to6 *s = NULL;

	HASH_FIND(hh, *hash, ip4, 4 * sizeof(uint8_t), s);
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
	while (mapping_count--) {
		uint8_t *addr4 = (uint8_t *)pos;
		pos += 4;
		uint8_t *addr6 = (uint8_t *)pos;
		pos += 16;
		add_ip4to6(&config->hash4to6, addr4, addr6);
		add_ip6to4(&config->hash6to4, addr6, addr4);
		// lpm_insert(&config->map4to6, 4, from, from, addr6);
	}

	*new_config = &config->config;

	return 0;
}

static int
nat64_handle_v6(struct ip4to6 **hash, struct packet *packet) {
    struct rte_mbuf *mbuf = packet_to_mbuf(packet);

    struct rte_ipv6_hdr *ipv6Header = rte_pktmbuf_mtod_offset(
        mbuf, struct rte_ipv6_hdr *, packet->network_header.offset
    );

	if (ipv6Header->proto == IPPROTO_FRAGMENT) {
		return -1; // Пропускаем фрагментированные пакеты
	}

    struct ip4to6 *new_src_addr = find_ip6to4(hash, (uint8_t *)&ipv6Header->src_addr);
    if (NULL == new_src_addr) {
        // Если не найдено соответствующего IPv4-адреса, ничего не делаем
        return 0;
    }

    struct rte_ipv4_hdr *new_ipv4_header;
    uint16_t new_packet_len = mbuf->pkt_len - sizeof(struct rte_ipv6_hdr) + sizeof(struct rte_ipv4_hdr);

    // Выделяем память для нового IPv4-заголовка
    if (rte_pktmbuf_prepend(mbuf, sizeof(struct rte_ipv4_hdr)) == NULL) {
        return -1;
    }

    new_ipv4_header = rte_pktmbuf_mtod(mbuf, struct rte_ipv4_hdr *);
    memset(new_ipv4_header, 0, sizeof(struct rte_ipv4_hdr));

    // Заполняем IPv4-заголовок
    new_ipv4_header->version_ihl = RTE_IPV4_VHL_DEF;
    new_ipv4_header->type_of_service = ipv6Header->vtc_flow;
    new_ipv4_header->total_length = rte_cpu_to_be_16(new_packet_len);
    new_ipv4_header->packet_id = 0; // TODO: Реализовать генерацию уникального ID пакета
    new_ipv4_header->fragment_offset = 0; // Флагы фрагментации
    new_ipv4_header->time_to_live = ipv6Header->hop_limits;
    new_ipv4_header->next_proto_id = ipv6Header->proto;

    memcpy(&new_ipv4_header->src_addr, new_src_addr->ip4, sizeof(struct in_addr));

    struct ip4to6 *new_dst_addr = find_ip6to4(hash, (uint8_t *)&ipv6Header->dst_addr);
    if (NULL == new_dst_addr) {
        // Если не найдено соответствующего IPv4-адреса, ничего не делаем
        return 0;
    }

    memcpy(&new_ipv4_header->dst_addr, new_dst_addr->ip4, sizeof(struct in_addr));

    // Обновляем CRC и другие заголовки (например, TCP/UDP)
    uint16_t old_csum = ipv6Header->payload_len;
    uint16_t new_csum = rte_ipv4_cksum(new_ipv4_header);
    if (new_ipv4_header->next_proto_id == IPPROTO_TCP) {
        struct rte_tcp_hdr *tcp_header = (struct rte_tcp_hdr *)(mbuf->pkt_data + sizeof(struct rte_ipv4_hdr));
        tcp_header->cksum = 0;
        tcp_header->cksum = rte_ipv4_udptcp_cksum(new_ipv4_header, tcp_header);
    } else if (new_ipv4_header->next_proto_id == IPPROTO_UDP) {
        struct rte_udp_hdr *udp_header = (struct rte_udp_hdr *)(mbuf->pkt_data + sizeof(struct rte_ipv4_hdr));
        udp_header->dgram_cksum = 0;
        udp_header->dgram_cksum = rte_ipv4_udptcp_cksum(new_ipv4_header, udp_header);
    } else if (new_ipv4_header->next_proto_id == IPPROTO_ICMP) {
        struct rte_icmp_hdr *icmp_header = (struct rte_icmp_hdr *)(mbuf->pkt_data + sizeof(struct rte_ipv4_hdr));
        icmp_header->icmp_cksum = 0;
        icmp_header->icmp_cksum = rte_ipv4_icmp_checksum(new_ipv4_header, icmp_header);
    }

    return 0;
}

static int
nat64_handle_v4(
	struct hash_entry *hash,
	struct packet *packet)
{
	if (packet->flags & PKT_FLAG_FRAG) {
		packet_front_drop(packet);
		return -1; // Неподдерживаемые фрагментированные пакеты
	}

	struct rte_mbuf *mbuf = packet_to_mbuf(packet);

	struct rte_ipv4_hdr *ipv4Header = rte_pktmbuf_mtod(mbuf, struct rte_ipv4_hdr *);
	if (!rte_is_valid_ipv4(ipv4Header)) {
		packet_front_drop(packet);
		return -1; // Неверный IPv4 заголовок
	}

	uint32_t new_packet_len = mbuf->pkt_len + sizeof(struct in6_addr) - sizeof(uint32_t);
	struct rte_mbuf *new_mbuf = rte_pktmbuf_alloc(packet_to_mempool(packet));
	if (!new_mbuf) {
		packet_front_drop(packet);
		return -1; // Не удалось выделить память
	}

	rte_pktmbuf_prepend(new_mbuf, sizeof(struct rte_ipv6_hdr));
	struct rte_ipv6_hdr *new_ipv6_header = rte_pktmbuf_mtod(mbuf, struct rte_ipv6_hdr *);
	memset(new_ipv6_header, 0, sizeof(struct rte_ipv6_hdr));

	new_ipv6_header->version_ihl = RTE_IPV6_VHL_DEF;
	new_ipv6_header->vtc_flow = ipv4Header->type_of_service;
	new_ipv6_header->payload_len = rte_cpu_to_be_16(new_packet_len);
	new_ipv6_header->hop_limits = ipv4Header->time_to_live;
	new_ipv6_header->proto = ipv4Header->next_proto_id;

	struct hash_entry *entry = find_ip4to6(hash, &ipv4Header->src_addr);
	if (!entry) {
		packet_front_drop(packet);
		return 0; // Не найдено соответствующего IPv6-адреса
	}

	memcpy(&new_ipv6_header->src_addr, entry->ipv6.s6_addr, sizeof(struct in6_addr));

	entry = find_ip4to6(hash, &ipv4Header->dst_addr);
	if (!entry) {
		packet_front_drop(packet);
		return 0; // Не найдено соответствующего IPv6-адреса
	}

	memcpy(&new_ipv6_header->dst_addr, entry->ipv6.s6_addr, sizeof(struct in6_addr));

	uint16_t old_csum = ipv4Header->total_length;
	uint16_t new_csum = rte_ipv6_cksum(new_ipv6_header);

	if (ipv4Header->next_proto_id == IPPROTO_TCP) {
		struct rte_tcp_hdr *tcp_header = (struct rte_tcp_hdr *)(mbuf->pkt_data + sizeof(struct rte_ipv4_hdr));
		tcp_header->cksum = 0;
		tcp_header->cksum = rte_ipv6_udptcp_cksum(new_ipv6_header, tcp_header);
	} else if (ipv4Header->next_proto_id == IPPROTO_UDP) {
		struct rte_udp_hdr *udp_header = (struct rte_udp_hdr *)(mbuf->pkt_data + sizeof(struct rte_ipv4_hdr));
		udp_header->dgram_cksum = 0;
		udp_header->dgram_cksum = rte_ipv6_udptcp_cksum(new_ipv6_header, udp_header);
	} else if (ipv4Header->next_proto_id == IPPROTO_ICMP) {
		struct rte_icmp_hdr *icmp_header = (struct rte_icmp_hdr *)(mbuf->pkt_data + sizeof(struct rte_ipv4_hdr));
		icmp_header->icmp_cksum = 0;
		icmp_header->icmp_cksum = rte_ipv6_icmp_checksum(new_ipv6_header, icmp_header);
	}

	rte_pktmbuf_free(mbuf);
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
	module->module.handler = balancer_handle_packets;
	module->module.config_handler = balancer_handle_configure;

	return &module->module;
}
