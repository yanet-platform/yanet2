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

	// Validate that the RX device ID exists in the configuration.
	// If not found, return the original TX device ID unchanged.
	if (packet->rx_device_id >= config->device_count) {
		return packet->tx_device_id;
	}

	struct forward_device_config *fdc =
		&config->device_forwards[packet->rx_device_id];

	// Perform LPM lookup on the destination IPv4 address using the
	// LPM table associated with the RX device to determine the
	// target forwarding device ID.
	uint32_t forward_device_id =
		lpm_lookup(&fdc->lpm_v4, 4, (uint8_t *)&header->dst_addr);

	// If the LPM lookup fails, it indicates no forwarding rule
	// is configured for this destination. Return the original
	// TX device ID to maintain current packet flow.
	if (forward_device_id == LPM_VALUE_INVALID) {
		return packet->tx_device_id;
	}

	struct forward_target *target =
		&ADDR_OF(&fdc->targets)[forward_device_id];

	// Update the forwarding counter for the RX device to target device
	// mapping to track packet statistics.
	uint64_t counter_id = target->counter_id;
	uint64_t *counters =
		counter_get_address(counter_id, worker_id, counter_storage);
	counters[0] += 1;

	// Return the target device ID for packet forwarding based on
	// the LPM lookup result.
	return target->device_id;
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

	// Validate that the RX device ID exists in the configuration.
	// If not found, return the original TX device ID unchanged.
	if (packet->rx_device_id >= config->device_count)
		return packet->tx_device_id;

	struct forward_device_config *fdc =
		&config->device_forwards[packet->rx_device_id];

	// Perform LPM lookup on the destination IPv6 address using the
	// LPM table associated with the RX device to determine the
	// target forwarding device ID.
	uint32_t forward_device_id =
		lpm_lookup(&fdc->lpm_v6, 16, (uint8_t *)&header->dst_addr);

	// If the LPM lookup fails, it indicates no forwarding rules
	// are configured for this destination. Return the original
	// TX device ID to maintain current packet flow.
	if (forward_device_id == LPM_VALUE_INVALID) {
		return packet->tx_device_id;
	}

	struct forward_target *target =
		&ADDR_OF(&fdc->targets)[forward_device_id];

	// Update the forwarding counter for the RX device to target device
	// mapping to track packet statistics.
	uint64_t counter_id = target->counter_id;
	uint64_t *counters =
		counter_get_address(counter_id, worker_id, counter_storage);
	counters[0] += 1;

	// Return the target device ID for packet forwarding based on
	// the LPM lookup result.
	return target->device_id;
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

	if (packet->rx_device_id >= config->device_count)
		return packet->tx_device_id;

	struct forward_device_config *fdc =
		&config->device_forwards[packet->rx_device_id];

	uint64_t *counters = counter_get_address(
		fdc->l2_counter_id, worker_id, counter_storage
	);
	counters[0] += 1;

	return fdc->l2_dst_device_id;
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
			// If the forwarding module modifies the target
			// device_id, the packet should be placed in the bypass
			// queue, effectively skipping subsequent pipeline
			// modules. A worker thread will later process the
			// bypass queue and transmit the packet to its intended
			// destination.
			packet->tx_device_id = device_id;
			packet_front_bypass(packet_front, packet);
		} else {
			// If the forwarding module doesn't modify the target
			// device_id, the packet should be placed in the output
			// queue, which will be the input queue for the next
			// module.
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
