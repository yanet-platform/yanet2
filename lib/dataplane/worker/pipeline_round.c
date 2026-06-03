#include "pipeline_round.h"

#include "common/memory_address.h"
#include "lib/controlplane/config/zone.h"
#include "lib/dataplane/config/zone.h"
#include "lib/dataplane/module/packet_front.h"
#include "lib/dataplane/pipeline/econtext.h"
#include "lib/dataplane/pipeline/pipeline.h"
#include "lib/dataplane/pipeline/schedule.h"

void
worker_pipeline_round(
	struct dp_worker *dp_worker,
	struct cp_config_gen *cp_config_gen,
	struct config_gen_ectx *config_gen_ectx,
	struct packet_front *packet_front
) {
	uint64_t device_count =
		cp_config_gen->device_registry.registry.capacity;

	struct device_ectx *devices[device_count];
	for (uint64_t idx = 0; idx < device_count; ++idx) {
		devices[idx] = ADDR_OF(config_gen_ectx->devices + idx);
	}

	while (1) {
		struct packet *packet;

		int empty = 1;

		while ((packet = packet_list_pop(&packet_front->pending_input)
		       ) != NULL) {
			empty = 0;
			device_ectx_schedule_input(
				devices[packet->tx_device_id], packet
			);
		}

		for (uint64_t idx = 0; idx < device_count; ++idx) {
			if (devices[idx] != NULL) {
				device_ectx_process_input(
					dp_worker, devices[idx], packet_front
				);
			}
		}

		while ((packet = packet_list_pop(&packet_front->pending_output)
		       ) != NULL) {
			empty = 0;
			device_ectx_schedule_output(
				devices[packet->tx_device_id], packet
			);
		}

		for (uint64_t idx = 0; idx < device_count; ++idx) {
			if (devices[idx] != NULL) {
				device_ectx_process_output(
					dp_worker, devices[idx], packet_front
				);
			}
		}

		if (empty) {
			break;
		}
	}
}
