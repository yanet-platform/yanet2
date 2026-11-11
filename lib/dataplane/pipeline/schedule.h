#pragma once

#include "common/rlist.h"
#include "lib/counters/counters.h"
#include "lib/dataplane/packet/data.h"
#include "lib/dataplane/pipeline/econtext.h"

// Count a packet dropped after exhausting its device recirculation budget.
//
// Counter slot 0 stores packets. Slot 1 stores the full packet length across
// all chained mbufs, unlike the first-segment byte tallies on packet fronts.
static inline void
device_entry_ectx_count_recirc_drop(
	struct device_entry_ectx *entry_ectx, const struct packet *packet
) {
	uint64_t *counter =
		counter_handle_get_value(entry_ectx->counter_packet_recirc_drop
		);
	counter[0] += 1;
	counter[1] += rte_pktmbuf_pkt_len(packet_to_mbuf(packet));
}

// Build the generation's device-entry worklists once.
//
// Must run on the owning worker before any packet is scheduled. Every
// entry is linked onto the home list, each device's input entry
// before its output one. After the first build the lists are
// permanent state and preparation does nothing: the round itself
// drains the home list onto its untouched list and works entries
// back home.
static inline void
config_gen_ectx_schedules_prepare(struct config_gen_ectx *config_gen_ectx) {
	if (config_gen_ectx->schedules_ready) {
		return;
	}

	rlist_init(&config_gen_ectx->entry_list);
	rlist_init(&config_gen_ectx->ready_list);

	for (uint64_t idx = 0; idx < config_gen_ectx->device_count; ++idx) {
		struct device_ectx *device_ectx =
			config_gen_ectx_get_device(config_gen_ectx, idx);
		if (device_ectx == NULL) {
			continue;
		}

		rlist_add(
			&config_gen_ectx->entry_list,
			&device_ectx->abs_input_pipelines->schedule_node
		);
		device_ectx->abs_input_pipelines->schedule_list =
			device_entry_schedule_home;

		rlist_add(
			&config_gen_ectx->entry_list,
			&device_ectx->abs_output_pipelines->schedule_node
		);
		device_ectx->abs_output_pipelines->schedule_list =
			device_entry_schedule_home;
	}

	config_gen_ectx->schedules_ready = 1;
}

// Schedule a packet onto a device entry's input list, moving the entry
// onto the round's ready list when needed.
//
// An entry anywhere but ready — home, or already drained onto the
// round's untouched list — moves to the tail of ready so the round
// runs it, reproducing the recirculation semantics; an entry already
// queued stays where it is. Removal needs no list head, so the entry
// may sit on any of the round's lists. Packets are only scheduled
// from inside a worker round, whose preparation built the worklists
// before any of them, so no readiness check is needed here.
static inline void
device_entry_ectx_schedule(
	struct config_gen_ectx *config_gen_ectx,
	struct device_entry_ectx *device_entry_ectx,
	struct packet *packet
) {
	if (device_entry_ectx->schedule_list != device_entry_schedule_ready) {
		rlist_remove(&device_entry_ectx->schedule_node);
		rlist_add(
			&config_gen_ectx->ready_list,
			&device_entry_ectx->schedule_node
		);
		device_entry_ectx->schedule_list = device_entry_schedule_ready;
	}

	packet_front_input(&device_entry_ectx->schedule, packet);
}

// Route a packet to a target device's input entry, counting it as
// pending_input on the originating schedule.
//
// The entry comes from the module's device target table, so it is never
// NULL here; the packet lands on its schedule input so the next round
// picks it up. Input and output routes share the packet lineage budget;
// an exhausted budget drops on the originating schedule and increments
// the target entry's input_recirc_drop counter. The pending counters
// count every route attempt, including dropped packets.
static inline void
module_ectx_route_input(
	struct module_ectx *module_ectx,
	struct packet_front *packet_front,
	struct device_entry_ectx *entry_ectx,
	struct packet *packet
) {
	packet_front->pending_input_count += 1;
	packet_front->pending_input_bytes += packet->data_len;

	if (!packet_recirc_try_redirect(
		    packet, module_ectx->packet_recirc_limit
	    )) {
		device_entry_ectx_count_recirc_drop(entry_ectx, packet);
		packet_front_drop(packet_front, packet);
		return;
	}
	device_entry_ectx_schedule(
		module_ectx->abs_config_gen_ectx, entry_ectx, packet
	);
}

// Route a packet to a target device's output entry, counting it as
// pending_output on the originating schedule.
//
// Symmetric to module_ectx_route_input: the packet is placed on the
// target device's output-pipelines schedule.
static inline void
module_ectx_route_output(
	struct module_ectx *module_ectx,
	struct packet_front *packet_front,
	struct device_entry_ectx *entry_ectx,
	struct packet *packet
) {
	packet_front->pending_output_count += 1;
	packet_front->pending_output_bytes += packet->data_len;

	if (!packet_recirc_try_redirect(
		    packet, module_ectx->packet_recirc_limit
	    )) {
		device_entry_ectx_count_recirc_drop(entry_ectx, packet);
		packet_front_drop(packet_front, packet);
		return;
	}
	device_entry_ectx_schedule(
		module_ectx->abs_config_gen_ectx, entry_ectx, packet
	);
}
