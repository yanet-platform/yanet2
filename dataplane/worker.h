#ifndef WORKER_H
#define WORKER_H

#include "pipeline.h"

// Read callback provided by dataplane
typedef uint16_t (*worker_read_func)(
	void *data,
	struct rte_mbuf **mbufs,
	uint16_t mbuf_count);

// write callback provided by dataplane
typedef uint16_t (*worker_write_func)(
	void *data,
	struct rte_mbuf **mbufs,
	uint16_t mbuf_count);

struct worker {
	worker_read_func read_func;
	void *read_data;
	worker_write_func write_func;
	void *write_data;

	uint16_t read_size;
	uint16_t write_size;

	bool stop;

};

void
worker_exec(
	struct pipeline *pipeline,
	worker_read_func read_func, void *read_data,
	worker_write_func write_func, void *write_data);

#endif
