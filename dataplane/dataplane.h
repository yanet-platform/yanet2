#ifndef DATAPLANE_H
#define DATAPLANE_H

#include <stdint.h>

#include "worker.h"
#include "pipeline.h"

struct dataplane_config {

};

struct dataplane {
	struct pipeline *pipeline;
	struct worker *workers;
	uint32_t worker_count;
};


int
dpdk_init();

#endif
