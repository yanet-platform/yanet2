#include "config.h"

#include <rte_ether.h>
#include <rte_ip.h>

#include "common/container_of.h"
#include "common/lpm.h"

#include "dataplane/module/module.h"
#include "dataplane/packet/packet.h"

static uint16_t
forward_handle_v4(
	struct dp_config *dp_config,
	struct forward_module_config *config,
	uint64_t worker_id,
	struct counter_storage *counter_storage,
	struct packet *packet
) {

	(void)dp_config;

	struct rte_mbuf *mbuf = packet_to_mbuf(packet);

	struct rte_ipv4_hdr *header = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_ipv4_hdr *, packet->network_header.offset
	);

	if (packet->tx_device_id >= config->device_count)
		return packet->tx_device_id;

	uint32_t forward_device_id = lpm_lookup(
		&config->device_forwards[packet->tx_device_id].lpm_v4,
		4,
		(uint8_t *)&header->dst_addr
	);

	if (forward_device_id == LPM_VALUE_INVALID) {
		return packet->tx_device_id;
	}

	uint64_t counter_id =
		ADDR_OF(&config->device_forwards[packet->tx_device_id].targets
		)[forward_device_id]
			.counter_id;
	uint64_t *counters = counter_get_address(
		ADDR_OF(&config->cp_module.counters.links) + counter_id,
		counter_storage,
		worker_id
	);
	counters[0] += 1;

	return ADDR_OF(&config->device_forwards[packet->tx_device_id].targets
	)[forward_device_id]
		.device_id;
}

static uint16_t
forward_handle_v6(
	struct dp_config *dp_config,
	struct forward_module_config *config,
	uint64_t worker_id,
	struct counter_storage *counter_storage,
	struct packet *packet
) {
	(void)dp_config;

	struct rte_mbuf *mbuf = packet_to_mbuf(packet);

	struct rte_ipv6_hdr *header = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_ipv6_hdr *, packet->network_header.offset
	);

	if (packet->tx_device_id >= config->device_count)
		return packet->tx_device_id;

	uint32_t forward_device_id = lpm_lookup(
		&config->device_forwards[packet->tx_device_id].lpm_v6,
		16,
		(uint8_t *)&header->dst_addr
	);

	if (forward_device_id == LPM_VALUE_INVALID) {
		return packet->tx_device_id;
	}

	uint64_t counter_id =
		ADDR_OF(&config->device_forwards[packet->tx_device_id].targets
		)[forward_device_id]
			.counter_id;
	uint64_t *counters = counter_get_address(
		ADDR_OF(&config->cp_module.counters.links) + counter_id,
		counter_storage,
		worker_id
	);
	counters[0] += 1;

	return ADDR_OF(&config->device_forwards[packet->tx_device_id].targets
	)[forward_device_id]
		.device_id;
}

static uint16_t
forward_handle_l2(
	struct dp_config *dp_config,
	struct forward_module_config *config,
	uint64_t worker_id,
	struct counter_storage *counter_storage,
	struct packet *packet
) {
	(void)dp_config;

	if (packet->tx_device_id >= config->device_count)
		return packet->tx_device_id;

	uint64_t *counters = counter_get_address(
		ADDR_OF(&config->cp_module.counters.links
		) + config->device_forwards[packet->tx_device_id].l2_counter_id,
		counter_storage,
		worker_id
	);
	counters[0] += 1;

	return config->device_forwards[packet->tx_device_id].l2_dst_device_id;
}

static void
forward_handle_packets(
	struct dp_config *dp_config,
	uint64_t worker_idx,
	struct cp_module *cp_module,
	struct counter_storage *counter_storage,
	struct packet_front *packet_front
) {
	(void)worker_idx;
	(void)counter_storage;

	struct forward_module_config *forward_config = container_of(
		cp_module, struct forward_module_config, cp_module
	);

	struct packet *packet;
	while ((packet = packet_list_pop(&packet_front->input)) != NULL) {
		uint16_t device_id = packet->tx_device_id;

		if (packet->network_header.type ==
		    rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4)) {
			device_id = forward_handle_v4(
				dp_config,
				forward_config,
				worker_idx,
				counter_storage,
				packet
			);
		} else if (packet->network_header.type ==
			   rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6)) {
			device_id = forward_handle_v6(
				dp_config,
				forward_config,
				worker_idx,
				counter_storage,
				packet
			);
		} else {
			device_id = forward_handle_l2(
				dp_config,
				forward_config,
				worker_idx,
				counter_storage,
				packet
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
