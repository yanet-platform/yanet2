#include "worker.h"
#include "common/memory_address.h"

#include "../../lib/controlplane/config/zone.h"
#include "dataplane/pipeline/pipeline.h"
#include "packet.h"

////////////////////////////////////////////////////////////////////////////////

struct packet_handle_result
yanet_worker_mock_handle_packets(
	struct yanet_worker_mock *worker, struct packet_list *input_packets
) {
	struct dp_config *dp_config = worker->dp_config;
	struct cp_config *cp_config = worker->cp_config;
	struct cp_config_gen *cp_config_gen =
		ADDR_OF(&cp_config->cp_config_gen);
	struct config_gen_ectx *config_gen_ectx =
		ADDR_OF(&cp_config_gen->config_gen_ectx);

	// do not update worker gen as it set to very big number previously

	struct packet_handle_result result;
	packet_list_init(&result.output_packets);
	packet_list_init(&result.drop_packets);

	if (config_gen_ectx == NULL) {
		packet_list_concat(&result.drop_packets, input_packets);
		packet_list_init(input_packets);
	}

	struct packet_front packet_front;
	packet_front_init(&packet_front);

	while (packet_list_first(input_packets)) {
		struct packet *packet = packet_list_pop(input_packets);
		packet->pipeline_ectx = NULL;

		struct device_ectx *device_ectx =
			ADDR_OF(config_gen_ectx->devices + packet->rx_device_id
			);
		if (device_ectx == NULL) {
			packet_front_drop(&packet_front, packet);
			continue;
		}

		device_ectx_process_input(
			&worker->dp_worker, device_ectx, &packet_front, packet
		);
	}

	// Now group packets by pipeline and build packet_front
	while (packet_list_first(&packet_front.pending)) {
		struct packet *packet =
			packet_list_first(&packet_front.pending);
		struct pipeline_ectx *pipeline_ectx = packet->pipeline_ectx;

		struct packet_list pending_packets;
		packet_list_init(&pending_packets);

		while ((packet = packet_list_pop(&packet_front.pending))) {
			if (packet->pipeline_ectx == pipeline_ectx) {
				packet_front_output(&packet_front, packet);
			} else {
				packet_list_add(&pending_packets, packet);
			}
		}

		/*
		 * All the packets with the same pipeline_ectx are ready to
		 * process, so return postponned packet into pending
		 * queue.
		 */
		packet_list_concat(&packet_front.pending, &pending_packets);

		pipeline_ectx_process(
			dp_config,
			&worker->dp_worker,
			cp_config_gen,
			pipeline_ectx,
			&packet_front
		);

		packet_list_concat(&result.drop_packets, &packet_front.drop);
		packet_list_init(&packet_front.drop);
		packet_list_concat(
			&result.output_packets, &packet_front.output
		);
		packet_list_init(&packet_front.output);
	}

	return result;
}