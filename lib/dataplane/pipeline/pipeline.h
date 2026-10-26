#pragma once

#include <stdint.h>

struct dp_config;
struct dp_worker;
struct packet_front;

struct device_ectx;

void
device_ectx_process_input(
	struct dp_worker *dp_worker,
	struct device_ectx *device_ectx,
	struct packet_front *packet_front
);

void
device_ectx_process_output(
	struct dp_worker *dp_worker,
	struct device_ectx *device_ectx,
	struct packet_front *packet_front
);
