#include "pipeline_round.h"
#include "pipeline_ready.h"

#include "common/container_of.h"
#include "lib/controlplane/config/econtext.h"
#include "lib/controlplane/config/zone.h"
#include "lib/counters/counters.h"
#include "lib/dataplane/config/zone.h"
#include "lib/dataplane/module/packet_front.h"
#include "lib/dataplane/packet/packet.h"
#include "lib/dataplane/pipeline/econtext.h"
#include "lib/dataplane/pipeline/pipeline.h"
#include "lib/dataplane/pipeline/schedule.h"

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
	struct packet_list *buckets = config_gen_ectx->abs_tx_buckets;
	uint64_t dropped_packets = 0;
	uint64_t dropped_bytes = 0;

	// The whole entry shares one egress: its device resolves through the
	// backing table once, and every packet finishing here is bound for
	// that port. A device with no port drops its finishers, counted on
	// the entry's drop counter.
	uint16_t port = UINT16_MAX;
	if (entry_ectx->device_id < config_gen_ectx->device_count) {
		port = config_gen_ectx
			       ->abs_egress_backing[entry_ectx->device_id];
	}
	struct packet_list *bucket =
		(port == UINT16_MAX) ? NULL : buckets + port;

	struct packet *packet;
	while ((packet = packet_list_pop(&packet_front->output)) != NULL) {
		if (bucket == NULL) {
			packet_list_add(&packet_front->drop, packet);
			dropped_packets += 1;
			dropped_bytes += packet->data_len;
		} else {
			packet_list_add(bucket, packet);
		}
	}

	packet_front->drop_count += dropped_packets;
	packet_front->drop_bytes += dropped_bytes;
	packet_front->output_count = 0;
	packet_front->output_bytes = 0;

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

// Runs only when the cached earliest deadline is due, so an idle round
// pays one comparison; see pipeline_round.h for the full contract.
void
worker_pipeline_round_arm_due(
	struct config_gen_ectx *config_gen_ectx,
	struct rlist *untouched,
	uint64_t current_time_ns
) {
	if (current_time_ns < config_gen_ectx->nearest_deadline_ns) {
		return;
	}

	uint64_t nearest = DEVICE_ENTRY_DEADLINE_NEVER;
	struct rlist *node = rlist_first(untouched);
	while (node != untouched) {
		struct rlist *next = node->next;
		struct device_entry_ectx *device_entry_ectx = container_of(
			node, struct device_entry_ectx, schedule_node
		);

		if (device_entry_ectx->deadline_ns <= current_time_ns) {
			device_entry_ectx_move_ready(
				config_gen_ectx, device_entry_ectx
			);
		} else if (device_entry_ectx->deadline_ns < nearest) {
			nearest = device_entry_ectx->deadline_ns;
		}

		node = next;
	}

	config_gen_ectx->nearest_deadline_ns = nearest;
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

	worker_pipeline_round_arm_due(
		config_gen_ectx, &untouched, dp_worker->current_time
	);

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
