#include "pipeline_round.h"

#include "common/container_of.h"
#include "lib/controlplane/config/econtext.h"
#include "lib/controlplane/config/zone.h"
#include "lib/dataplane/config/zone.h"
#include "lib/dataplane/module/packet_front.h"
#include "lib/dataplane/pipeline/econtext.h"
#include "lib/dataplane/pipeline/pipeline.h"

// Run one device entry on its own schedule.
//
// The batch is detached so redirects land in the reusable inbox; input
// entry processing has no packet transmission allowed, so its whole
// output is dropped and the only chance for a packet to survive is
// being routed into a device entry by a module.
static inline void
worker_run_entry(
	struct dp_worker *dp_worker,
	struct device_entry_ectx *device_entry_ectx,
	struct packet_front *packet_front
) {
	struct device_ectx *device_ectx = device_entry_ectx->abs_device_ectx;
	struct packet_front *schedule = &device_entry_ectx->schedule;

	struct packet_front active = *schedule;
	packet_front_init(schedule);

	if (device_entry_ectx->direction == device_entry_direction_input) {
		device_ectx_process_input(dp_worker, device_ectx, &active);
		packet_front_drop_output(&active);
	} else {
		device_ectx_process_output(dp_worker, device_ectx, &active);
	}

	packet_front_merge(packet_front, &active);
}

static inline void
worker_pipeline_round_process_ready(
	struct dp_worker *dp_worker,
	struct config_gen_ectx *config_gen_ectx,
	struct packet_front *packet_front
) {
	/*
	 * Run the ready entries until the packets are exhausted.
	 * Each entry moves back to the home list before running,
	 * so a packet routed into it while it runs moves it to
	 * the tail of ready and it runs again within the same
	 * round.
	 */
	while (!rlist_empty(&config_gen_ectx->ready_list)) {
		struct rlist *node = rlist_first(&config_gen_ectx->ready_list);
		rlist_remove(node);

		struct device_entry_ectx *device_entry_ectx = container_of(
			node, struct device_entry_ectx, schedule_node
		);
		rlist_add(&config_gen_ectx->entry_list, node);
		device_entry_ectx->schedule_list = device_entry_schedule_home;

		worker_run_entry(dp_worker, device_entry_ectx, packet_front);
	}
}

void
worker_pipeline_round(
	struct dp_worker *dp_worker,
	struct cp_config_gen *cp_config_gen,
	struct config_gen_ectx *config_gen_ectx,
	struct packet_front *packet_front
) {
	(void)cp_config_gen;

	// The untouched list holds every entry no packet reached this
	// round: the round drains the home list onto it at constant
	// cost, in home-list order. Entries the receive path already
	// scheduled sit on the ready list and miss the drain.
	struct rlist untouched;
	rlist_init(&untouched);
	rlist_concat(&untouched, &config_gen_ectx->entry_list);

	worker_pipeline_round_process_ready(
		dp_worker, config_gen_ectx, packet_front
	);

	if (rlist_empty(&untouched)) {
		return;
	}

	rlist_concat(&config_gen_ectx->ready_list, &untouched);

	worker_pipeline_round_process_ready(
		dp_worker, config_gen_ectx, packet_front
	);
}
