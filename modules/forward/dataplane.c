#include "config.h"

#include <rte_ether.h>
#include <rte_ip.h>

#include "common/container_of.h"
#include "common/lpm.h"

#include "dataplane/module/module.h"
#include "dataplane/packet/packet.h"

static inline uint16_t
forward_select_device(struct dp_config *dp_config, uint16_t device_id) {
	if (device_id >= dp_config->dp_topology.device_count)
		return device_id;

	uint16_t *forward_map = DECODE_ADDR(
		&dp_config->dp_topology, dp_config->dp_topology.forward_map
	);

	return forward_map[device_id];
}

static uint32_t
forward_handle_v4(
	struct dp_config *dp_config,
	struct forward_module_config *config,
	struct packet *packet
) {

	(void)dp_config;

	struct rte_mbuf *mbuf = packet_to_mbuf(packet);

	struct rte_ipv4_hdr *header = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_ipv4_hdr *, packet->network_header.offset
	);

	if (lpm_lookup(&config->lpm_v4, 4, (uint8_t *)&header->dst_addr) !=
	    LPM_VALUE_INVALID) {
		return forward_select_device(dp_config, packet->tx_device_id);
	}

	return packet->tx_device_id;
}

static uint32_t
forward_handle_v6(
	struct dp_config *dp_config,
	struct forward_module_config *config,
	struct packet *packet
) {
	(void)dp_config;

	struct rte_mbuf *mbuf = packet_to_mbuf(packet);

	struct rte_ipv6_hdr *header = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_ipv6_hdr *, packet->network_header.offset
	);

	if (lpm_lookup(&config->lpm_v6, 16, header->dst_addr) !=
	    LPM_VALUE_INVALID) {
		return forward_select_device(dp_config, packet->tx_device_id);
	}

	return packet->tx_device_id;
}

static void
forward_handle_packets(
	struct dp_config *dp_config,
	struct module_data *module_data,
	struct packet_front *packet_front
) {
	struct forward_module_config *forward_config = container_of(
		module_data, struct forward_module_config, module_data
	);

	struct packet *packet;
	while ((packet = packet_list_pop(&packet_front->input)) != NULL) {
		uint16_t device_id = packet->tx_device_id;

		if (packet->network_header.type ==
		    rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4)) {
			device_id = forward_handle_v4(
				dp_config, forward_config, packet
			);
		} else if (packet->network_header.type ==
			   rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6)) {
			device_id = forward_handle_v6(
				dp_config, forward_config, packet
			);
		} else {
			device_id = forward_select_device(
				dp_config, packet->tx_device_id
			);
		}

		if (device_id != packet->tx_device_id) {
			packet->tx_device_id = device_id;
			packet_front_bypass(packet_front, packet);
		} else {
			packet_front_output(packet_front, packet);
		}
	}
}

struct forward_module {
	struct module module;
};

struct module *
new_module_forward() {
	struct forward_module *module =
		(struct forward_module *)malloc(sizeof(struct forward_module));

	if (module == NULL) {
		return NULL;
	}

	snprintf(
		module->module.name,
		sizeof(module->module.name),
		"%s",
		"forward"
	);
	module->module.handler = forward_handle_packets;

	return &module->module;
}
