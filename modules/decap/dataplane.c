#include "dataplane.h"

#include "config.h"

#include "dataplane/module/module.h"

#include "dataplane/packet/decap.h"

#include "rte_ether.h"
#include "rte_ip.h"

struct decap_module {
	struct module module;
};

static int
decap_handle_v4(const struct lpm *lpm, struct packet *packet) {
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);

	struct rte_ipv4_hdr *ipv4_hdr = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_ipv4_hdr *, packet->network_header.offset
	);
	if (ipv4_hdr->fragment_offset != 0) {
		return -1; // Fragmented packet
	}

	if (lpm_lookup(lpm, 4, (uint8_t *)&ipv4_hdr->dst_addr) !=
	    LPM_VALUE_INVALID) {
		return packet_decap(packet);
	}

	return 0;
}

static int
decap_handle_v6(const struct lpm *lpm, struct packet *packet) {
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);

	struct rte_ipv6_hdr *ipv6_hdr = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_ipv6_hdr *, packet->network_header.offset
	);
	if (ipv6_hdr->proto == IPPROTO_FRAGMENT) {
		return -1; // Fragmented packet
	}

	if (lpm_lookup(lpm, 16, (uint8_t *)&ipv6_hdr->dst_addr) !=
	    LPM_VALUE_INVALID) {
		packet->flow_label =
			rte_be_to_cpu_32(ipv6_hdr->vtc_flow) & 0x000FFFFF;
		return packet_decap(packet);
	}

	return 0;
}

void
decap_handle_packets(
	struct dp_config *dp_config,
	struct module_data *module_data,
	struct packet_front *packet_front
) {
	(void)dp_config;
	struct decap_module_config *decap_config = container_of(
		module_data, struct decap_module_config, module_data
	);

	struct packet *packet;
	while ((packet = packet_list_pop(&packet_front->input)) != NULL) {
		int result = 0;
		if (packet->network_header.type ==
		    rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4)) {
			result = decap_handle_v4(
				&decap_config->prefixes4, packet
			);
		} else if (packet->network_header.type ==
			   rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6)) {
			result = decap_handle_v6(
				&decap_config->prefixes6, packet
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
new_module_decap() {
	struct decap_module *module =
		(struct decap_module *)malloc(sizeof(struct decap_module));

	if (module == NULL) {
		return NULL;
	}

	snprintf(
		module->module.name, sizeof(module->module.name), "%s", "decap"
	);
	module->module.handler = decap_handle_packets;

	return &module->module;
}
