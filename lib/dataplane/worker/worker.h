#pragma once

#include "lib/dataplane/config/zone.h"

struct packet *
worker_packet_alloc(struct dp_worker *worker);

// Deep-copy a packet and partition its remaining recirculation credits.
//
// The source keeps the odd credit so best-effort mirroring cannot consume it.
// Allocation failure leaves the source packet unchanged.
struct packet *
worker_clone_packet(
	struct dp_worker *dp_worker,
	struct packet *packet,
	uint16_t packet_recirc_limit
);

void
worker_packet_free(struct packet *packet);
