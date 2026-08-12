#include "pipeline_round.h"

#include "lib/controlplane/config/econtext.h"
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
	(void)cp_config_gen;

	uint64_t device_count = config_gen_ectx->device_count;

	/*
	 * Force a full traversal on the first iteration.
	 *
	 * Every device, and through it every pipeline, function, chain and
	 * module, runs once per tick even when no packets arrived, so periodic
	 * work has a chance to make progress. Later iterations only revisit
	 * devices whose input or output entry received packets, whether
	 * straight from RX or routed in by a module.
	 */
	int force_poll = 1;

	while (1) {
		if (!force_poll) {
			int has_work = 0;
			for (uint64_t idx = 0; idx < device_count; ++idx) {
				struct device_ectx *device_ectx =
					config_gen_ectx_get_device(
						config_gen_ectx, idx
					);
				if (device_ectx == NULL) {
					continue;
				}
				if (packet_list_first(
					    &ADDR_OF(&device_ectx
							      ->input_pipelines)
						     ->schedule.input
				    ) != NULL ||
				    packet_list_first(
					    &ADDR_OF(&device_ectx
							      ->output_pipelines
					    )
						     ->schedule.input
				    ) != NULL) {
					has_work = 1;
					break;
				}
			}
			if (!has_work) {
				break;
			}
		}

		for (uint64_t idx = 0; idx < device_count; ++idx) {
			struct device_ectx *device_ectx =
				config_gen_ectx_get_device(
					config_gen_ectx, idx
				);
			if (device_ectx == NULL) {
				continue;
			}

			struct packet_front *schedule =
				&ADDR_OF(&device_ectx->input_pipelines)
					 ->schedule;

			if (!force_poll &&
			    packet_list_first(&schedule->input) == NULL) {
				continue;
			}

			device_ectx_process_input(
				dp_worker, device_ectx, schedule
			);

			/*
			 * Input entry point processing has no packet
			 * transmission allowed so drop the whole output.
			 * The only chance for a packet to survive is being
			 * routed into a device entry by a module.
			 */
			packet_front_drop_output(schedule);

			packet_front_merge(packet_front, schedule);
		}

		for (uint64_t idx = 0; idx < device_count; ++idx) {
			struct device_ectx *device_ectx =
				config_gen_ectx_get_device(
					config_gen_ectx, idx
				);
			if (device_ectx == NULL) {
				continue;
			}

			struct packet_front *schedule =
				&ADDR_OF(&device_ectx->output_pipelines)
					 ->schedule;

			if (!force_poll &&
			    packet_list_first(&schedule->input) == NULL) {
				continue;
			}

			device_ectx_process_output(
				dp_worker, device_ectx, schedule
			);

			packet_front_merge(packet_front, schedule);
		}

		force_poll = 0;
	}
}
