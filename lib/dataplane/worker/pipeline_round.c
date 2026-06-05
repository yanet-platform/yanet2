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

	struct device_ectx *devices[device_count];
	struct packet_front *input[device_count];
	struct packet_front *output[device_count];
	for (uint64_t idx = 0; idx < device_count; ++idx) {
		struct device_ectx *device =
			ADDR_OF(config_gen_ectx->devices + idx);
		input[idx] = device != NULL ? ADDR_OF(&device->pending_input) +
						      dp_worker->idx
					    : NULL;
		output[idx] = device != NULL ? ADDR_OF(&device->pending_output
					       ) + dp_worker->idx
					     : NULL;
		devices[idx] = device;

		// Initialize each pending front on first use. The controlplane
		// only zeroes them: an initialized front stores a pointer into
		// itself, valid only in the process that wrote it.
		if (device != NULL && input[idx]->output.last == NULL) {
			packet_front_init(input[idx]);
			packet_front_init(output[idx]);
		}
	}

	while (1) {
		struct packet *packet;

		int empty = 1;

		while ((packet = packet_list_pop(&packet_front->pending_input)
		       ) != NULL) {
			empty = 0;
			packet_front_output(
				input[packet->tx_device_id], packet
			);
		}

		while ((packet = packet_list_pop(&packet_front->pending_output)
		       ) != NULL) {
			empty = 0;
			packet_front_output(
				output[packet->tx_device_id], packet
			);
		}

		if (empty) {
			break;
		}

		for (uint64_t idx = 0; idx < device_count; ++idx) {
			if (input[idx] != NULL &&
			    input[idx]->output.count > 0) {
				device_ectx_process_input(
					dp_worker, devices[idx], input[idx]
				);
				packet_front_merge(packet_front, input[idx]);
				packet_front_init(input[idx]);
			}
		}

		for (uint64_t idx = 0; idx < device_count; ++idx) {
			if (output[idx] != NULL &&
			    output[idx]->output.count > 0) {
				device_ectx_process_output(
					dp_worker, devices[idx], output[idx]
				);
				packet_front_merge(packet_front, output[idx]);
				packet_front_init(output[idx]);
			}
		}
	}
}
