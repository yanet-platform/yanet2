#pragma once

#include "dataplane/packet/packet.h"
#include "mock/mock.h"

#include "config.h"
#include "modules/balancer/bench/alloc.h"

struct bench {
	struct yanet_mock yanet;
	struct diag diag;
	void *shared_memory;
	size_t total_memory;
	struct allocator alloc;
};

int
bench_init(struct bench *bench, struct bench_config *config);

void *
bench_shared_memory(struct bench *bench);

const char *
bench_take_error(struct bench *bench);

void
bench_free(struct bench *bench);

int
bench_handle_packets(
	struct bench *bench,
	size_t worker,
	struct packet_list *packets_batch,
	size_t batches_count
);

uint8_t *
bench_alloc(void *bench, size_t align, size_t size);
