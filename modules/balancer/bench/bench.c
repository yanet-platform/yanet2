#include "bench.h"
#include "controlplane/diag/diag.h"
#include "mock/config.h"
#include "mock/mock.h"
#include "mock/packet.h"
#include <assert.h>
#include <string.h>
#include <sys/mman.h>

#define DP_MEMORY (1 << 20)

int
bench_init(struct bench *bench, struct bench_config *config) {
	// Initialize fields to safe defaults before any operation that might
	// fail
	memset(&bench->yanet, 0, sizeof(bench->yanet));
	bench->shared_memory = NULL;
	bench->total_memory = 0;

	diag_reset(&bench->diag);

	if (config->total_memory < DP_MEMORY + config->cp_memory) {
		NEW_ERROR(
			"memory is to small (required at least %lu)",
			DP_MEMORY + config->cp_memory
		);
		goto error;
	}

	errno = 0;
	void *shared_memory =
		mmap(NULL,
		     config->total_memory,
		     PROT_READ | PROT_WRITE,
		     MAP_PRIVATE | MAP_ANONYMOUS | MAP_HUGETLB,
		     -1,
		     0);
	if (shared_memory == MAP_FAILED) {
		NEW_ERROR("mmap failed: %s", strerror(errno));
		goto error;
	}

	memset(shared_memory, 0, config->total_memory);

	bench->shared_memory = shared_memory;
	bench->total_memory = config->total_memory;

	struct yanet_mock_config yanet_config = {
		.worker_count = config->workers,
		.device_count = 1,
		.dp_memory = DP_MEMORY,
		.cp_memory = config->cp_memory,
		.devices = {(struct yanet_mock_device_config){
			.id = 0,
			.name = "01:00.0",
		}}
	};

	if (yanet_mock_init(&bench->yanet, &yanet_config, shared_memory) != 0) {
		NEW_ERROR("failed to init mock");
		goto error_unmap;
	}

	allocator_init(
		&bench->alloc,
		shared_memory + DP_MEMORY + config->cp_memory,
		config->total_memory - config->cp_memory - DP_MEMORY
	);

	return 0;

error_unmap:
	munmap(shared_memory, config->total_memory);

error:
	diag_fill(&bench->diag);
	return -1;
}

#undef DP_MEMORY

const char *
bench_take_error(struct bench *bench) {
	return diag_take_msg(&bench->diag);
}

void
bench_free(struct bench *bench) {
	yanet_mock_free(&bench->yanet);
	munmap(bench->shared_memory, bench->total_memory);
}

int
bench_handle_packets(
	struct bench *bench,
	size_t worker,
	struct packet_list *packets_batch,
	size_t batches_count
) {
	struct packet_handle_result result;
	size_t dropped_count = 0;
	for (size_t i = 0; i < batches_count; i++) {
		memset(&result, 0, sizeof(result));
		yanet_mock_handle_packets(
			&bench->yanet, packets_batch + i, worker, &result
		);
		dropped_count += result.drop_packets.count;
	}
	return dropped_count > 0;
}

uint8_t *
bench_alloc(void *bench, size_t align, size_t size) {
	struct bench *b = (struct bench *)bench;
	return allocator_alloc(&b->alloc, align, size);
}

void *
bench_shared_memory(struct bench *bench) {
	return bench->shared_memory;
}