#pragma once

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

// Route a packet to its target device's input entry, counting it as
// pending_input on the originating schedule.
//
// The packet lands on the target device entry's schedule input so the next
// round picks it up. When the target device is absent from this generation
// the packet is dropped on the originating schedule instead of being
// stranded. packet->tx_device_id must already name the destination.
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
	packet_front_input(
		&ADDR_OF(&device_ectx->input_pipelines)->schedule, packet
	);
}

// Route a packet to its target device's output entry, counting it as
// pending_output on the originating schedule.
//
// Symmetric to module_ectx_route_input: the packet is placed on the target
// device's output-pipelines schedule, or dropped on the originating schedule
// when the device is gone. packet->tx_device_id must already name the
// destination.
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
	packet_front_input(
		&ADDR_OF(&device_ectx->output_pipelines)->schedule, packet
	);
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
