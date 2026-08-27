#pragma once

#include <stdint.h>

#include "lib/dataplane/packet/packet.h"
#include "lib/dataplane/worker/tx_pipe.h"

struct dp_worker;

// One worker's pipes toward a single destination device.
//
// A packet is spread across them by its flow hash, so every packet of a flow
// reaches the same consumer and cannot be reordered against its own.
struct worker_tx_connection {
	uint32_t count;
	struct worker_tx_pipe *pipes;
};

// Hold a packet bound for another worker until the round ends.
//
// Packets accumulate on the pipe their flow hashes to, so a whole burst
// crosses to its consumer together rather than one packet at a time. A packet
// addressed to a device this worker cannot reach, and any the pipe later
// refuses, joins the caller's failure list, which the round discards.
// Recovering a refused packet reads it back from the buffer it carries, so it
// must live at the head of that buffer — where the pipeline puts it.
void
worker_tx_stage(
	struct worker_tx_connection *connections,
	uint32_t device_count,
	struct dp_worker *dp_worker,
	struct packet *packet,
	struct packet_list *failed
);

// Hand every pipe's held packets to their consumers.
//
// Must run before the round ends, or packets staged during it would wait for
// traffic that may never come.
void
worker_tx_flush_all(
	struct worker_tx_connection *connections,
	uint32_t device_count,
	struct dp_worker *dp_worker,
	struct packet_list *failed
);
