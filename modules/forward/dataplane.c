#include "dataplane.h"

#include <rte_ether.h>
#include <rte_ip.h>

#include "container_of.h"
#include "lpm.h"

#include "dataplane/module/module.h"
#include "dataplane/packet/packet.h"

struct forward_module_config {
	struct module_config config;

	struct lpm lpm_v4;
	struct lpm lpm_v6;

	uint16_t route[8];
};

static uint32_t
forward_handle_v4(struct forward_module_config *config, struct packet *packet) {
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);

	struct rte_ipv4_hdr *header = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_ipv4_hdr *, packet->network_header.offset
	);

	if (lpm_lookup(&config->lpm_v4, 4, (uint8_t *)&header->dst_addr) !=
	    LPM_VALUE_INVALID) {
		return config->route[packet->tx_device_id];
	}

	return packet->tx_device_id;
}

static uint32_t
forward_handle_v6(struct forward_module_config *config, struct packet *packet) {
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);

	struct rte_ipv6_hdr *header = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_ipv6_hdr *, packet->network_header.offset
	);

	if (lpm_lookup(&config->lpm_v6, 16, header->dst_addr) !=
	    LPM_VALUE_INVALID) {
		return config->route[packet->tx_device_id];
	}

	return packet->tx_device_id;
}

static void
forward_handle_packets(
	struct module *module,
	struct module_config *config,
	struct packet_front *packet_front
) {
	(void)module;
	struct forward_module_config *forward_config =
		container_of(config, struct forward_module_config, config);

	struct packet *packet;
	while ((packet = packet_list_pop(&packet_front->input)) != NULL) {
		uint16_t device_id = packet->tx_device_id;

		if (packet->network_header.type ==
		    rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4)) {
			device_id = forward_handle_v4(forward_config, packet);
		} else if (packet->network_header.type ==
			   rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6)) {
			device_id = forward_handle_v6(forward_config, packet);
		} else {
			device_id = forward_config->route[packet->tx_device_id];
		}

		if (device_id != packet->tx_device_id) {
			packet->tx_device_id = device_id;
			packet_front_bypass(packet_front, packet);
		} else {
			packet_front_output(packet_front, packet);
		}
	}
}

static int
forward_handle_configure(
	struct module *module,
	const void *config_data,
	size_t config_data_size,
	struct module_config **new_config
) {

	(void)module;

	struct forward_module_config *config = (struct forward_module_config *)
		malloc(sizeof(struct forward_module_config));

	lpm_init(&config->lpm_v4);
	lpm_init(&config->lpm_v6);

	lpm_insert(
		&config->lpm_v4,
		4,
		(uint8_t[4]){0, 0, 0, 0},
		(uint8_t[4]){0xff, 0xff, 0xff, 0xff},
		1
	);

	lpm_insert(
		&config->lpm_v6,
		16,
		(uint8_t[16]){0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
		(uint8_t[16]){0xff,
			      0xff,
			      0xff,
			      0xff,
			      0xff,
			      0xff,
			      0xff,
			      0xff,
			      0xff,
			      0xff,
			      0xff,
			      0xff,
			      0xff,
			      0xff,
			      0xff,
			      0xff},
		1
	);

	memcpy(config->route, config_data, config_data_size);

	*new_config = &config->config;

	return 0;
};

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
	module->module.config_handler = forward_handle_configure;

	return &module->module;
}
