#pragma once

#include "lib/dataplane/config/zone.h"

struct packet *
worker_packet_alloc(struct dp_worker *worker);

// Deep-copy a packet and its current recirculation counters.
//
// The clone spends its copied budget independently; no aggregate counter is
// shared with the source packet.
struct packet *
worker_clone_packet(struct dp_worker *dp_worker, struct packet *packet);

void
worker_packet_free(struct packet *packet);
