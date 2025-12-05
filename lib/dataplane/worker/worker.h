#pragma once

#include "dataplane/config/zone.h"

struct packet *
worker_packet_alloc(struct dp_worker *worker);

struct packet *
worker_clone_packet(struct dp_worker *dp_worker, struct packet *packet);