#include "config.h"

#include <rte_ether.h>
#include <rte_ip.h>
#include <rte_tcp.h>
#include <rte_udp.h>

#include "dataplane/packet/encap.h"

#include "dataplane/config/zone.h"
#include "state.h"

struct balancer_module {
	struct module module;
};

int
balancer_vs_lookup_v4(
	struct balancer_module_config *balancer_config,
	struct packet *packet,
	struct balancer_vs **res_vs
) {
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);

	struct rte_ipv4_hdr *ipv4_hdr = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_ipv4_hdr *, packet->network_header.offset
	);

	uint32_t service_id = lpm_lookup(
		&balancer_config->v4_service_lookup,
		4,
		(uint8_t *)&ipv4_hdr->dst_addr
	);

	if (service_id == LPM_VALUE_INVALID)
		return -1;

	if (balancer_config->service_count <= service_id)
		// If the service_id is out of range of available
		// services
		return -1;

	struct balancer_vs **vs_ptr =
		ADDR_OF(&balancer_config->services) + service_id;
	struct balancer_vs *vs = ADDR_OF(vs_ptr);

	if (lpm_lookup(&vs->src, 4, (uint8_t *)&ipv4_hdr->src_addr) ==
	    LPM_VALUE_INVALID)
		return -1;
	/*
	 * FIXME: lpm value is 4 byte long where service_id is 8 bytes but
	 * it is less possible to have more thant UINT32_MAX services.
	 */
	*res_vs = vs;
	return 0;
}

int
balancer_vs_lookup_v6(
	struct balancer_module_config *balancer_config,
	struct packet *packet,
	struct balancer_vs **res_vs
) {
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);

	struct rte_ipv6_hdr *ipv6_hdr = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_ipv6_hdr *, packet->network_header.offset
	);

	uint32_t service_id = lpm_lookup(
		&balancer_config->v6_service_lookup,
		16,
		(uint8_t *)&ipv6_hdr->dst_addr
	);

	if (service_id == LPM_VALUE_INVALID)
		return -1;

	if (balancer_config->service_count <= service_id)
		// If the service_id is out of range of available
		// services
		return -1;

	struct balancer_vs **vs_ptr =
		ADDR_OF(&balancer_config->services) + service_id;
	struct balancer_vs *vs = ADDR_OF(vs_ptr);

	if (lpm_lookup(&vs->src, 16, (uint8_t *)&ipv6_hdr->src_addr) ==
	    LPM_VALUE_INVALID)
		return -1;

	/*
	 * FIXME: lpm value is 4 byte long where service_id is 8 bytes but
	 * it is less possible to have more thant UINT32_MAX services.
	 */
	*res_vs = vs;
	return 0;
}

struct packet_metadata {
	uint8_t network_proto;
	uint8_t transport_proto;

	uint8_t src_addr[16];
	uint8_t dst_addr[16];
	uint16_t src_port;
	uint16_t dst_port;

	uint8_t tcp_flags;
};

int
balancer_fill_packet_metadata(
	struct packet *packet, struct packet_metadata *metadata
) {
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);

	if (packet->network_header.type ==
	    rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4)) {
		metadata->network_proto = METADATA_NETWORK_PROTO_V4;
		struct rte_ipv4_hdr *ipv4_header = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_ipv4_hdr *,
			packet->network_header.offset
		);

		memcpy(metadata->dst_addr, (uint8_t *)&ipv4_header->dst_addr, 4
		);
		memcpy(metadata->src_addr, (uint8_t *)&ipv4_header->src_addr, 4
		);
	} else if (packet->network_header.type ==
		   rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6)) {
		metadata->network_proto = METADATA_NETWORK_PROTO_V6;
		struct rte_ipv6_hdr *ipv6_header = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_ipv6_hdr *,
			packet->network_header.offset
		);

		memcpy(metadata->dst_addr, ipv6_header->dst_addr, 16);
		memcpy(metadata->src_addr, ipv6_header->src_addr, 16);
	} else {
		return -1;
	}
	if (packet->transport_header.type == IPPROTO_TCP) {
		metadata->transport_proto = METADATA_TRANSPORT_PROTO_TCP;
		struct rte_tcp_hdr *tcp_header = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_tcp_hdr *,
			packet->transport_header.offset
		);

		metadata->dst_port = tcp_header->dst_port;
		metadata->src_port = tcp_header->src_port;
		metadata->tcp_flags = tcp_header->tcp_flags;
	} else if (packet->transport_header.type == IPPROTO_UDP) {
		metadata->transport_proto = METADATA_TRANSPORT_PROTO_UDP;
		struct rte_udp_hdr *udp_header = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_udp_hdr *,
			packet->transport_header.offset
		);

		metadata->dst_port = udp_header->dst_port;
		metadata->src_port = udp_header->src_port;
	} else {
		return -1;
	}
	return 0;
}

static inline bool
metadata_reschedule_real(struct packet_metadata *metadata) {
	if (metadata->transport_proto == METADATA_TRANSPORT_PROTO_UDP) {
		return true;
	}
	return metadata->transport_proto == METADATA_TRANSPORT_PROTO_TCP &&
	       ((metadata->tcp_flags & (RTE_TCP_SYN_FLAG | RTE_TCP_RST_FLAG)) ==
		RTE_TCP_SYN_FLAG);
}

static inline uint32_t
metadata_storage_timeout(
	struct balancer_state_config *state_config,
	struct packet_metadata *metadata
) {
	if (metadata->transport_proto == METADATA_TRANSPORT_PROTO_UDP) {
		return state_config->udp_timeout;
	}
	if (metadata->transport_proto != METADATA_TRANSPORT_PROTO_TCP) {
		return state_config->default_timeout;
	}

	if ((metadata->tcp_flags & RTE_TCP_SYN_FLAG) == RTE_TCP_SYN_FLAG) {
		if ((metadata->tcp_flags & RTE_TCP_ACK_FLAG) ==
		    RTE_TCP_ACK_FLAG) {
			return state_config->tcp_syn_ack_timeout;
		}
		return state_config->tcp_syn_timeout;
	}
	if (metadata->tcp_flags & RTE_TCP_FIN_FLAG) {
		return state_config->tcp_fin_timeout;
	}
	return state_config->tcp_timeout;
}

static inline struct balancer_rs *
balancer_rs_lookup(
	struct balancer_module_config *config,
	struct balancer_vs *vs,
	struct packet *packet
) {
	struct packet_metadata metadata;
	int res = balancer_fill_packet_metadata(packet, &metadata);
	if (res != 0) {
		return NULL;
	}

	uint32_t timeout =
		metadata_storage_timeout(&config->state_config, &metadata);

	struct balancer_rs *reals = ADDR_OF(&config->reals);

	uint32_t real_id = balancer_state_lookup(&config->state, &metadata);

	if (real_id != SESSION_VALUE_INVALID) {
		struct balancer_rs *balancer_rs = reals + real_id;
		if (balancer_rs->weight > 0) {
			res = balancer_state_touch(
				&config->state, &metadata, timeout
			);
			if (res != 0) {
				return NULL;
			}
			return balancer_rs;
		}
		if (!metadata_reschedule_real(&metadata)) {
			return NULL;
		}
	}

	uint32_t real_idx = ring_get(&vs->real_ring, packet->hash);
	if (real_idx == RING_VALUE_INVALID)
		return NULL;

	real_id = real_idx + vs->real_start;

	res = balancer_state_set(&config->state, &metadata, timeout, real_id);
	if (res != 0) {
		return NULL;
	}

	return reals + real_id;
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
			struct rte_mbuf *mbuf = packet_to_mbuf(packet);

			struct rte_ipv4_hdr *ipv4_header =
				rte_pktmbuf_mtod_offset(
					mbuf,
					struct rte_ipv4_hdr *,
					packet->network_header.offset
				);
			uint32_t src_mask = *(uint32_t *)(&rs->src_mask[0]);
			uint32_t src_addr = *(uint32_t *)(&rs->src_addr[0]);
			// rs->src_addr is already masked.
			uint32_t src =
				(ipv4_header->src_addr & ~src_mask) | src_addr;

			return packet_ip4_encap(
				packet, rs->dst_addr, (uint8_t *)(&src)
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
			for (uint8_t i = 0; i < 16; i++) {
				// rs->src_addr is already masked.
				src[i] = (ipv6_header->src_addr[i] &
					  (~rs->src_mask[i])) |
					 rs->src_addr[i];
			}

			return packet_ip6_encap(packet, rs->dst_addr, src);
		}
	}

	return -1;
}

void
balancer_handle_packets(
	struct dp_config *dp_config,
	uint64_t worker_idx,
	struct cp_module *cp_module,
	struct counter_storage *counter_storage,
	struct packet_front *packet_front
) {
	(void)dp_config;
	(void)worker_idx;
	(void)counter_storage;

	struct balancer_module_config *balancer_config = container_of(
		cp_module, struct balancer_module_config, cp_module
	);

	struct packet *packet;
	while ((packet = packet_list_pop(&packet_front->input)) != NULL) {
		int res = -1;
		struct balancer_vs *vs;
		if (packet->network_header.type ==
		    rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4)) {
			res = balancer_vs_lookup_v4(
				balancer_config, packet, &vs
			);
		} else if (packet->network_header.type ==
			   rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6)) {
			res = balancer_vs_lookup_v6(
				balancer_config, packet, &vs
			);
		}

		if (res != 0) {
			packet_front_drop(packet_front, packet);
			continue;
		}

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
