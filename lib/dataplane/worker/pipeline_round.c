#include "pipeline_round.h"

#include "common/container_of.h"
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

	struct rlist *active_head =
		&config_gen_ectx
			 ->schedule_lists[config_gen_ectx->schedule_active];
	struct rlist *inactive_head =
		&config_gen_ectx
			 ->schedule_lists[config_gen_ectx->schedule_active ^ 1];

	/*
	 * Every entry executes at least once per tick.
	 *
	 * Tick preparation hands over a list that still holds every
	 * entry, so all of them run even when no packets arrived and
	 * periodic work has a chance to make progress. Within the tick,
	 * an entry re-enters the list only when a packet is routed onto
	 * it, and the round ends once that quiesces.
	 */
	while (!rlist_empty(active_head)) {
		struct rlist *node = rlist_first(active_head);

		/*
		 * Park the entry before executing it: a packet routed
		 * into it while it runs moves it back onto the active
		 * list so it runs again this tick.
		 */
		rlist_remove(node);
		rlist_add(inactive_head, node);

		struct device_entry_ectx *device_entry_ectx = container_of(
			node, struct device_entry_ectx, schedule_node
		);
		device_entry_ectx->schedule_list =
			config_gen_ectx->schedule_active ^ 1;

		struct device_ectx *device_ectx =
			ADDR_OF(&device_entry_ectx->device_ectx);
		struct packet_front *schedule = &device_entry_ectx->schedule;

		// Detach the batch so redirects land in the reusable
		// inbox.
		struct packet_front active = *schedule;
		packet_front_init(schedule);

		if (device_entry_ectx->direction ==
		    device_entry_direction_input) {
			device_ectx_process_input(
				dp_worker, device_ectx, &active
			);

			/*
			 * Input entry point processing has no packet
			 * transmission allowed so drop the whole output.
			 * The only chance for a packet to survive is being
			 * routed into a device entry by a module.
			 */
			packet_front_drop_output(&active);
		} else {
			device_ectx_process_output(
				dp_worker, device_ectx, &active
			);
		}

		packet_front_merge(packet_front, &active);
	}
}
