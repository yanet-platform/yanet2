#include "config.h"

#include <rte_ether.h>
#include <rte_ip.h>
#include <rte_tcp.h>
#include <rte_udp.h>

#include "dataplane/packet/encap.h"

#include "dataplane/config/zone.h"

struct balancer_module {
	struct module module;
};

int
balancer_handle_v4(
	struct lpm *vs_lookup, struct packet *packet, uint64_t *res_service_id
) {
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);

	struct rte_ipv4_hdr *ipv4_hdr = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_ipv4_hdr *, packet->network_header.offset
	);

	uint32_t service_id =
		lpm_lookup(vs_lookup, 4, (uint8_t *)&ipv4_hdr->src_addr);

	if (service_id == LPM_VALUE_INVALID)
		return -1;

	/*
	 * FIXME: lpm value is 4 byte long where service_id is 8 bytes but
	 * it is less possible to have more thant UINT32_MAX services.
	 */
	*res_service_id = service_id;
	return 0;
}

int
balancer_handle_v6(
	struct lpm *vs_lookup, struct packet *packet, uint64_t *res_service_id
) {
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);

	struct rte_ipv6_hdr *ipv6_hdr = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_ipv6_hdr *, packet->network_header.offset
	);

	uint32_t service_id =
		lpm_lookup(vs_lookup, 16, (uint8_t *)&ipv6_hdr->src_addr);

	if (service_id == LPM_VALUE_INVALID)
		return -1;

	/*
	 * FIXME: lpm value is 4 byte long where service_id is 8 bytes but
	 * it is less possible to have more thant UINT32_MAX services.
	 */
	*res_service_id = service_id;
	return 0;
}

static inline struct balancer_rs *
balancer_rs_lookup(
	struct balancer_module_config *config,
	struct balancer_vs *vs,
	struct packet *packet
) {
	if (vs->real_count == 0)
		return NULL;

	struct rte_mbuf *mbuf = packet_to_mbuf(packet);
	uint16_t mlt = 0;
	if (packet->transport_header.type == IPPROTO_TCP) {
		struct rte_tcp_hdr *tcp_header = NULL;
		tcp_header = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_tcp_hdr *,
			packet->transport_header.offset
		);

		mlt = tcp_header->src_port;
	} else if (packet->transport_header.type == IPPROTO_UDP) {
		struct rte_udp_hdr *udp_header = NULL;
		udp_header = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_udp_hdr *,
			packet->transport_header.offset
		);

		mlt = udp_header->src_port;
	}

	return ADDR_OF(&config->reals) + vs->real_start +
	       (mlt % vs->real_count);
}

static int
balancer_route(
	struct balancer_module_config *config,
	struct balancer_vs *vs,
	struct balancer_rs *rs,
	struct packet *packet
) {
	(void)config;

	if (rs->type == RS_TYPE_V4) {
		if (vs->type & VS_OPT_ENCAP) {
			return packet_ip4_encap(
				packet, rs->dst_addr, rs->src_addr
			);
		}
	}

	if (rs->type == RS_TYPE_V6) {
		if (vs->type & VS_OPT_ENCAP) {
			struct rte_mbuf *mbuf = packet_to_mbuf(packet);

			struct rte_ipv6_hdr *ipv6_header =
				rte_pktmbuf_mtod_offset(
					mbuf,
					struct rte_ipv6_hdr *,
					packet->network_header.offset
				);

			uint8_t src[16];
			memcpy(src, rs->src_addr, 16);
			// FIXME randomize src
			src[14] ^= ipv6_header->src_addr[14];
			src[15] ^= ipv6_header->src_addr[15];

			return packet_ip6_encap(packet, rs->dst_addr, src);
		}
	}

	return -1;
}

static void
balancer_handle_packets(
	struct dp_config *dp_config,
	struct module_data *module_data,
	struct packet_front *packet_front
) {
	(void)dp_config;

	struct balancer_module_config *balancer_config = container_of(
		module_data, struct balancer_module_config, module_data
	);

	struct packet *packet;
	while ((packet = packet_list_pop(&packet_front->input)) != NULL) {
		int lookup = -1;
		uint64_t service_id;
		if (packet->network_header.type ==
		    rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4)) {
			lookup = balancer_handle_v4(
				&balancer_config->v4_service_lookup,
				packet,
				&service_id
			);
		} else if (packet->network_header.type ==
			   rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6)) {
			lookup = balancer_handle_v6(
				&balancer_config->v6_service_lookup,
				packet,
				&service_id
			);
		}

		if (!lookup) {
			packet_front_drop(packet_front, packet);
			continue;
		}

		struct balancer_vs *vs =
			ADDR_OF(&balancer_config->services) + service_id;

		struct balancer_rs *rs =
			balancer_rs_lookup(balancer_config, vs, packet);
		if (rs == NULL) {
			// real lookup failed
			packet_front_drop(packet_front, packet);
			return;
		}

		if (balancer_route(balancer_config, vs, rs, packet) != 0) {
			packet_front_drop(packet_front, packet);
			continue;
		}

		packet_front_output(packet_front, packet);
	}
}

struct module *
new_module_balancer() {
	struct balancer_module *module =
		(struct balancer_module *)malloc(sizeof(struct balancer_module)
		);

	if (module == NULL) {
		return NULL;
	}

	snprintf(
		module->module.name,
		sizeof(module->module.name),
		"%s",
		"balancer"
	);
	module->module.handler = balancer_handle_packets;

	return &module->module;
}
