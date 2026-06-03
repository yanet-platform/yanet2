#pragma once

#include "econtext.h"

static inline void
device_ectx_schedule_input(struct device_ectx *ectx, struct packet *packet) {
	packet_list_add(&ectx->pending_input.input, packet);
}

static inline void
device_ectx_schedule_output(struct device_ectx *ectx, struct packet *packet) {
	packet_list_add(&ectx->pending_output.input, packet);
}