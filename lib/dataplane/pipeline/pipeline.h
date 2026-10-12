#pragma once

#include <stdint.h>

struct dp_config;
struct dp_worker;
struct packet_list;

struct config_gen_ectx;
struct device_ectx;

void
config_gen_ectx_resolve_counters(struct config_gen_ectx *config_gen_ectx);

void
device_ectx_process_input(
	struct dp_worker *dp_worker,
	struct config_gen_ectx *config_gen_ectx,
	struct device_ectx *device_ectx,
	struct packet_list *input,
	uint64_t input_count,
	uint64_t input_bytes,
	struct packet_list *output_sink
);

void
device_ectx_process_output(
	struct dp_worker *dp_worker,
	struct config_gen_ectx *config_gen_ectx,
	struct device_ectx *device_ectx,
	struct packet_list *input,
	uint64_t input_count,
	uint64_t input_bytes,
	struct packet_list *output_sink
);
