#include "kernel.h"

#include "common/container_of.h"
#include "common/lpm.h"

#include "packet/packet.h"
#include "pipeline.h"

#include <rte_ether.h>
#include <rte_ip.h>

struct kernel_module_config {
	struct module_config config;

	struct lpm lpm_v4;
	struct lpm lpm_v6;

	uint16_t route[8];
};

static uint32_t
kernel_handle_v4(struct kernel_module_config *config, struct packet *packet) {
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
kernel_handle_v6(struct kernel_module_config *config, struct packet *packet) {
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
kernel_handle_packets(
	struct module *module,
	struct module_config *config,
	struct pipeline_front *pipeline_front
) {
	(void)module;
	struct kernel_module_config *kernel_config =
		container_of(config, struct kernel_module_config, config);

	struct packet *packet;
	while ((packet = packet_list_pop(&pipeline_front->input)) != NULL) {
		uint16_t device_id = packet->tx_device_id;
		;
		if (packet->network_header.type ==
		    rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4)) {
			device_id = kernel_handle_v4(kernel_config, packet);
		} else if (packet->network_header.type ==
			   rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6)) {
			device_id = kernel_handle_v6(kernel_config, packet);
		} else {
			device_id = kernel_config->route[packet->tx_device_id];
			//			pipeline_front_drop(pipeline_front,
			// packet); 			continue;
		}

		if (device_id != packet->tx_device_id) {
			packet->tx_device_id = device_id;
			pipeline_front_bypass(pipeline_front, packet);
		} else {
			pipeline_front_output(pipeline_front, packet);
		}
	}
}

static int
kernel_handle_configure(
	struct module *module,
	const char *config_name,
	const void *config_data,
	size_t config_data_size,
	struct module_config *old_config,
	struct module_config **new_config
) {

	(void)module;
	(void)config_data;
	(void)config_data_size;
	(void)old_config;
	(void)new_config;

	struct kernel_module_config *config = (struct kernel_module_config *)
		malloc(sizeof(struct kernel_module_config));

	snprintf(
		config->config.name,
		sizeof(config->config.name),
		"%s",
		config_name
	);

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

	memcpy(config->route, config_data, sizeof(uint16_t) * 8);

	*new_config = &config->config;

	return 0;
};

struct kernel_module {
	struct module module;
};

struct module *
new_module_kernel() {
	struct kernel_module *module =
		(struct kernel_module *)malloc(sizeof(struct kernel_module));

	if (module == NULL) {
		return NULL;
	}

	snprintf(
		module->module.name, sizeof(module->module.name), "%s", "kernel"
	);
	module->module.handler = kernel_handle_packets;
	module->module.config_handler = kernel_handle_configure;

	return &module->module;
}
