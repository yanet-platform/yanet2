#include "pipeline_round.h"

#include "common/container_of.h"
#include "lib/controlplane/config/econtext.h"
#include "lib/controlplane/config/zone.h"
#include "lib/counters/counters.h"
#include "lib/dataplane/config/zone.h"
#include "lib/dataplane/module/packet_front.h"
#include "lib/dataplane/packet/packet.h"
#include "lib/dataplane/pipeline/econtext.h"
#include "lib/dataplane/pipeline/pipeline.h"

static inline void
counter_add_packets_bytes(
	struct counter_value_handle *counter, int64_t packets, int64_t bytes
) {
	if (packets == 0) {
		return;
	}

	uint64_t *values = counter_handle_get_value(counter);
	values[0] += packets;
	values[1] += bytes;
}

void
worker_entry_terminal_egress(
	struct config_gen_ectx *config_gen_ectx,
	struct device_entry_ectx *entry_ectx,
	struct packet_front *packet_front
) {
	const uint16_t *backing = config_gen_ectx->abs_egress_backing;
	uint64_t dropped_packets = 0;
	uint64_t dropped_bytes = 0;

	struct packet_list survive;
	packet_list_init(&survive);

	struct packet *packet;
	while ((packet = packet_list_pop(&packet_front->output)) != NULL) {
		uint16_t port = UINT16_MAX;
		if (packet->tx_device_id < config_gen_ectx->device_count) {
			port = backing[packet->tx_device_id];
		}

		if (port == UINT16_MAX) {
			packet_list_add(&packet_front->drop, packet);
			dropped_packets += 1;
			dropped_bytes += packet->data_len;
		} else {
			packet_list_add(&survive, packet);
		}
	}

	packet_list_concat(&packet_front->output, &survive);
	packet_front->drop_count += dropped_packets;
	packet_front->drop_bytes += dropped_bytes;
	packet_front->output_count -= dropped_packets;
	packet_front->output_bytes -= dropped_bytes;

	if (dropped_packets != 0) {
		counter_add_packets_bytes(
			entry_ectx->counter_packet_drop,
			dropped_packets,
			dropped_bytes
		);
		// The entry's accounting already counted these packets as
		// handed to the next stage; a terminal drop means they never
		// reach transmit, so the transmit side of the accounting
		// gives them back.
		counter_add_packets_bytes(
			entry_ectx->counter_packet_tx,
			-(int64_t)dropped_packets,
			-(int64_t)dropped_bytes
		);
	}
}

// Run one device entry on its own schedule.
//
// The batch is detached so redirects land in the reusable inbox; input
// entry processing has no packet transmission allowed, so its whole
// output is dropped and the only chance for a packet to survive is
// being routed into a device entry by a module.
static inline void
worker_run_entry(
	struct dp_worker *dp_worker,
	struct config_gen_ectx *config_gen_ectx,
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
		worker_entry_terminal_egress(
			config_gen_ectx, device_entry_ectx, &active
		);
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

		worker_run_entry(
			dp_worker,
			config_gen_ectx,
			device_entry_ectx,
			packet_front
		);
	}
}

void
worker_pipeline_round(
	struct dp_worker *dp_worker,
	struct cp_config_gen *cp_config_gen,
	struct config_gen_ectx *config_gen_ectx
) {
	(void)cp_config_gen;

	// The round runs on the generation context's scratch front: it is
	// also the final drop list the chains splice their finished drop
	// lists into, so the caller must be the context's owner and drain
	// the front before the next round.
	struct packet_front *packet_front = &config_gen_ectx->packet_front;

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
