#pragma once

#include "common/memory_address.h"
#include "econtext.h"

static inline void
device_ectx_schedule_input(
	struct device_ectx *ectx, struct packet *packet, uint64_t worker_idx
) {
	struct packet_front *queue = ADDR_OF(&ectx->pending_input) + worker_idx;
	packet_list_add(&queue->input, packet);
}

static inline void
device_ectx_schedule_output(
	struct device_ectx *ectx, struct packet *packet, uint64_t worker_idx
) {
	struct packet_front *queue =
		ADDR_OF(&ectx->pending_output) + worker_idx;
	packet_list_add(&queue->input, packet);
}