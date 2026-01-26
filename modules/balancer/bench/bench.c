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
    diag_reset(&bench->diag);

    if (config->memory < DP_MEMORY) {
        NEW_ERROR("memory is to small (required at least %d)", DP_MEMORY);
        goto error;
    }

    errno = 0;
    void *shared_memory = mmap(NULL, config->memory,
                   PROT_READ | PROT_WRITE,
                   MAP_PRIVATE | MAP_ANONYMOUS | MAP_HUGETLB,
                   -1, 0);
    if (shared_memory == MAP_FAILED) {
        NEW_ERROR("mmap failed: %s", strerror(errno));
        goto error;
    }

    bench->shared_memory = shared_memory;

    struct yanet_mock_config yanet_config = {
        .worker_count = config->workers,
        .device_count = 1,
        .dp_memory = DP_MEMORY,
        .cp_memory = config->memory - DP_MEMORY,
        .devices = {
            (struct yanet_mock_device_config){
                .id = 0,
                .name = "device",
            }
        }
    };

    if (yanet_mock_init(&bench->yanet, &yanet_config, shared_memory) != 0) {
        NEW_ERROR("failed to init mock");
        goto error_unmap;
    }

    return 0;

error_unmap:
    munmap(shared_memory, config->memory);

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
    munmap(bench->shared_memory, bench->config->memory);
    yanet_mock_free(&bench->yanet);
}

int
bench_handle_packets(struct bench *bench, size_t worker, struct packet_list *packets_batch, size_t batches_count) {
    struct packet_handle_result result;
    size_t dropped_count = 0;
    for (size_t i = 0; i < batches_count; i++) {
        memset(&result, 0, sizeof(result));
        yanet_mock_handle_packets(&bench->yanet, packets_batch + i, worker, &result);
        dropped_count += result.drop_packets.count;
    }
    return dropped_count > 0;
}