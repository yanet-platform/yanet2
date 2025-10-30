#include "config.h"

#include <rte_ether.h>
#include <rte_ip.h>

#include "common/container_of.h"
#include "common/lpm.h"

#include "dataplane/module/module.h"
#include "dataplane/packet/packet.h"
#include "dataplane/pipeline/pipeline.h"

static struct forward_target *
forward_handle_v4(
	struct module_ectx *module_ectx,
	struct forward_module_config *config,
	struct packet *packet
) {
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);

	struct rte_ipv4_hdr *header = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_ipv4_hdr *, packet->network_header.offset
	);

	uint64_t src_device_idx =
		module_ectx_decode_device(module_ectx, packet->tx_device_id);

	// Validate that the RX device ID exists in the configuration.
	// If not found, return the original TX device ID unchanged.
	if (src_device_idx >= config->device_count) {
		return NULL;
	}

	struct forward_device_config **devices = ADDR_OF(&config->devices);

	struct forward_device_config *fdc = ADDR_OF(devices + src_device_idx);

	// Perform LPM lookup on the destination IPv4 address using the
	// LPM table associated with the RX device to determine the
	// target forwarding device ID.
	uint32_t forward_target_id =
		lpm_lookup(&fdc->lpm_v4, 4, (uint8_t *)&header->dst_addr);

	if (forward_target_id == LPM_VALUE_INVALID) {
		return NULL;
	}

	return ADDR_OF(&fdc->targets) + forward_target_id;
}

static struct forward_target *
forward_handle_v6(
	struct module_ectx *module_ectx,
	struct forward_module_config *config,
	struct packet *packet
) {
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);

	struct rte_ipv6_hdr *header = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_ipv6_hdr *, packet->network_header.offset
	);

	uint64_t src_device_idx =
		module_ectx_decode_device(module_ectx, packet->tx_device_id);

	// Validate that the RX device ID exists in the configuration.
	// If not found, return the original TX device ID unchanged.
	if (src_device_idx >= config->device_count) {
		return NULL;
	}

	struct forward_device_config **devices = ADDR_OF(&config->devices);

	struct forward_device_config *fdc = ADDR_OF(devices + src_device_idx);

	// Perform LPM lookup on the destination IPv6 address using the
	// LPM table associated with the RX device to determine the
	// target forwarding device ID.
	uint32_t forward_target_id =
		lpm_lookup(&fdc->lpm_v6, 16, (uint8_t *)&header->dst_addr);

	if (forward_target_id == LPM_VALUE_INVALID) {
		return NULL;
	}

	return ADDR_OF(&fdc->targets) + forward_target_id;
}

static struct forward_target *
forward_handle_l2(
	struct module_ectx *module_ectx,
	struct forward_module_config *config,
	struct packet *packet
) {
	uint64_t src_device_idx =
		module_ectx_decode_device(module_ectx, packet->tx_device_id);

	// Validate that the RX device ID exists in the configuration.
	// If not found, return the original TX device ID unchanged.
	if (src_device_idx >= config->device_count) {
		return NULL;
	}

	struct forward_device_config **devices = ADDR_OF(&config->devices);

	struct forward_device_config *fdc = ADDR_OF(devices + src_device_idx);

	if (fdc->l2_target_id == LPM_VALUE_INVALID) {
		return NULL;
	}
	return ADDR_OF(&fdc->targets) + fdc->l2_target_id;
}

static void
forward_handle_packets(
	struct dp_worker *dp_worker,
	struct module_ectx *module_ectx,
	struct packet_front *packet_front
) {
	struct forward_module_config *forward_config = container_of(
		ADDR_OF(&module_ectx->cp_module),
		struct forward_module_config,
		cp_module
	);

	struct packet *packet;
	while ((packet = packet_list_pop(&packet_front->input)) != NULL) {
		struct forward_target *target = NULL;

		if (packet->network_header.type ==
		    rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4)) {
			target = forward_handle_v4(
				module_ectx, forward_config, packet
			);
		} else if (packet->network_header.type ==
			   rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6)) {
			target = forward_handle_v6(
				module_ectx, forward_config, packet
			);
		} else {
			target = forward_handle_l2(
				module_ectx, forward_config, packet
			);
		}

		if (target != NULL) {
			struct config_gen_ectx *config_gen_ectx =
				ADDR_OF(&module_ectx->config_gen_ectx);

			uint64_t device_id = module_ectx_encode_device(
				module_ectx, target->device_id
			);

			struct device_ectx *device_ectx =
				config_gen_ectx_get_device(
					config_gen_ectx, device_id
				);
			if (device_ectx == NULL) {
				packet_front_drop(packet_front, packet);
			}

			uint64_t *counters = counter_get_address(
				target->counter_id,
				dp_worker->idx,
				ADDR_OF(&module_ectx->counter_storage)
			);
			counters[0] += 1;

			if (0) {
				device_ectx_process_input(
					dp_worker,
					device_ectx,
					packet_front,
					packet
				);
			} else {
				device_ectx_process_output(
					dp_worker,
					device_ectx,
					packet_front,
					packet
				);
			}

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
