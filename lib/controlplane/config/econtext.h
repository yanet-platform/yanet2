#pragma once

#include "lib/counters/counters.h"
#include "lib/dataplane/packet/data.h"
#include "lib/dataplane/pipeline/econtext.h"

#include "lib/errors/errors.h"

static inline struct device_ectx *
config_gen_ectx_get_device(
	struct config_gen_ectx *config_gen_ectx, uint64_t index
) {
	if (index >= config_gen_ectx->device_count) {
		return NULL;
	}
	return ADDR_OF(config_gen_ectx->devices + index);
}

// Return the object execution context installed at the given object index, or
// NULL when the index is out of range or no context was created for that slot.
static inline struct object_ectx *
config_gen_ectx_get_object(
	struct config_gen_ectx *config_gen_ectx, uint64_t index
) {
	if (index >= config_gen_ectx->object_count) {
		return NULL;
	}
	struct object_ectx **objects = ADDR_OF(&config_gen_ectx->objects);
	return ADDR_OF(objects + index);
}

// Count a packet dropped after exhausting its device recirculation budget.
//
// Counter slot 0 stores packets. Slot 1 stores the full packet length across
// all chained mbufs, unlike the first-segment byte tallies on packet fronts.
static inline void
device_entry_ectx_count_recirc_drop(
	struct device_entry_ectx *entry_ectx, const struct packet *packet
) {
	uint64_t *counter = counter_handle_get_value(
		ADDR_OF_NONNULL(&entry_ectx->counter_packet_recirc_drop)
	);
	counter[0] += 1;
	counter[1] += rte_pktmbuf_pkt_len(packet_to_mbuf(packet));
}

// Route a packet to its target device's input entry, counting it as
// pending_input on the originating schedule.
//
// The packet lands on the target device entry's schedule input so the next
// round picks it up. When the target device is absent from this generation
// the packet is dropped on the originating schedule instead of being
// stranded. Input and output routes share the packet lineage budget; an
// exhausted budget drops on the originating schedule and increments the target
// entry's input_recirc_drop counter. packet->tx_device_id names the
// destination. The pending counters count every route attempt, including
// dropped packets.
static inline void
module_ectx_route_input(
	struct module_ectx *module_ectx,
	struct packet_front *packet_front,
	struct packet *packet
) {
	packet_front->pending_input_count += 1;
	packet_front->pending_input_bytes += packet->data_len;

	struct config_gen_ectx *config_gen_ectx =
		ADDR_OF(&module_ectx->config_gen_ectx);
	struct device_ectx *device_ectx = config_gen_ectx_get_device(
		config_gen_ectx, packet->tx_device_id
	);
	if (device_ectx == NULL) {
		packet_front_drop(packet_front, packet);
		return;
	}
	struct device_entry_ectx *entry_ectx =
		ADDR_OF(&device_ectx->input_pipelines);
	if (!packet_recirc_try_redirect(
		    packet, module_ectx->packet_recirc_limit
	    )) {
		device_entry_ectx_count_recirc_drop(entry_ectx, packet);
		packet_front_drop(packet_front, packet);
		return;
	}
	packet_front_input(&entry_ectx->schedule, packet);
}

// Route a packet to its target device's output entry, counting it as
// pending_output on the originating schedule.
//
// Symmetric to module_ectx_route_input: the packet is placed on the target
// device's output-pipelines schedule, or dropped on the originating schedule
// when the device is gone. packet->tx_device_id must already name the
// destination. Input and output routes share the packet lineage budget; an
// exhausted budget drops on the originating schedule and increments the target
// entry's output_recirc_drop counter. The pending counters count every route
// attempt, including dropped packets.
static inline void
module_ectx_route_output(
	struct module_ectx *module_ectx,
	struct packet_front *packet_front,
	struct packet *packet
) {
	packet_front->pending_output_count += 1;
	packet_front->pending_output_bytes += packet->data_len;

	struct config_gen_ectx *config_gen_ectx =
		ADDR_OF(&module_ectx->config_gen_ectx);
	struct device_ectx *device_ectx = config_gen_ectx_get_device(
		config_gen_ectx, packet->tx_device_id
	);
	if (device_ectx == NULL) {
		packet_front_drop(packet_front, packet);
		return;
	}
	struct device_entry_ectx *entry_ectx =
		ADDR_OF(&device_ectx->output_pipelines);
	if (!packet_recirc_try_redirect(
		    packet, module_ectx->packet_recirc_limit
	    )) {
		device_entry_ectx_count_recirc_drop(entry_ectx, packet);
		packet_front_drop(packet_front, packet);
		return;
	}
	packet_front_input(&entry_ectx->schedule, packet);
}

// Build one execution context per worker.
//
// Returns an array of worker_count offset pointers, each a config_gen_ectx
// carrying that worker's own single-instance counter storages. Each entry is
// released with config_gen_ectx_free; the array itself is freed by the caller.
struct config_gen_ectx **
config_gen_ectxs_create(
	struct cp_config_gen *config_gen,
	struct cp_config_gen *old_config_gen,
	uint64_t worker_count,
	yanet_error **err
);

void
config_gen_ectx_free(
	struct cp_config_gen *config_gen,
	struct config_gen_ectx *config_gen_ectx
);
