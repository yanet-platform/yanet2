#include "dataplane.h"

#include "dataplane/packet/decap.h"

#include "rte_ether.h"
#include "rte_ip.h"

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
	struct module *module,
	struct module_config *config,
	struct packet_front *packet_front
) {
	(void)module;

	struct decap_module_config *decap_config =
		container_of(config, struct decap_module_config, config);

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

static int
decap_handle_configure(
	struct module *module,
	const void *config_data,
	size_t config_data_size,
	struct module_config **new_config
) {
	(void)module;

	struct decap_module_config *config = (struct decap_module_config *)
		malloc(sizeof(struct decap_module_config));

	// FIXME: handle errors
	lpm_init(&config->prefixes4);
	lpm_init(&config->prefixes6);

	// FIXME: deserialization
	uintptr_t pos = (uintptr_t)config_data;
	uintptr_t end = pos + config_data_size;
	// FIXME: check data boundaries
	(void)end;

	uint32_t v4_range_count = *(uint32_t *)pos;
	pos += sizeof(v4_range_count);
	while (v4_range_count--) {
		const uint8_t *from = (uint8_t *)pos;
		pos += 4;
		const uint8_t *to = (uint8_t *)pos;
		pos += 4;
		lpm_insert(&config->prefixes4, 4, from, to, 1);
	}

	uint32_t v6_range_count = *(uint32_t *)pos;
	pos += sizeof(v6_range_count);
	while (v6_range_count--) {
		const uint8_t *from = (uint8_t *)pos;
		pos += 16;
		const uint8_t *to = (uint8_t *)pos;
		pos += 16;
		lpm_insert(&config->prefixes6, 8, from, to, 1);
	}

	*new_config = &config->config;

	return 0;
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
	module->module.config_handler = decap_handle_configure;

	return &module->module;
}
