#include "pipeline_round.h"

#include "common/memory_address.h"
#include "lib/controlplane/config/zone.h"
#include "lib/dataplane/config/zone.h"
#include "lib/dataplane/module/packet_front.h"
#include "lib/dataplane/pipeline/econtext.h"
#include "lib/dataplane/pipeline/pipeline.h"

void
worker_pipeline_round(
	struct dp_worker *dp_worker,
	struct cp_config_gen *cp_config_gen,
	struct config_gen_ectx *config_gen_ectx,
	struct packet_front *packet_front
) {
	uint64_t device_count =
		cp_config_gen->device_registry.registry.capacity;

	while (1) {
		struct packet_front schedule_input[device_count];
		for (uint64_t idx = 0; idx < device_count; ++idx) {
			packet_front_init(schedule_input + idx);
		}

		struct packet_front schedule_output[device_count];
		for (uint64_t idx = 0; idx < device_count; ++idx) {
			packet_front_init(schedule_output + idx);
		}

		struct packet *packet;

		int empty = 1;

		while ((packet = packet_list_pop(&packet_front->pending_input)
		       ) != NULL) {
			empty = 0;
			packet_front_output(
				schedule_input + packet->tx_device_id, packet
			);
		}

		while ((packet = packet_list_pop(&packet_front->pending_output)
		       ) != NULL) {
			empty = 0;
			packet_front_output(
				schedule_output + packet->tx_device_id, packet
			);
		}

		if (empty) {
			break;
		}

		struct device_ectx **devices = config_gen_ectx->devices;

		for (uint64_t idx = 0; idx < device_count; ++idx) {
			if (packet_list_first(&schedule_input[idx].output) ==
			    NULL) {
				continue;
			}

			struct device_ectx *device_ectx =
				ADDR_OF(devices + idx);

			device_ectx_process_input(
				dp_worker, device_ectx, schedule_input + idx
			);

			packet_front_merge(packet_front, schedule_input + idx);
		}

		for (uint64_t idx = 0; idx < device_count; ++idx) {
			if (packet_list_first(&schedule_output[idx].output) ==
			    NULL) {
				continue;
			}

			struct device_ectx *device_ectx =
				ADDR_OF(devices + idx);

			device_ectx_process_output(
				dp_worker, device_ectx, schedule_output + idx
			);

			packet_front_merge(packet_front, schedule_output + idx);
		}
	}
}
