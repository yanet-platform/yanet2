#pragma once

#include <stddef.h>
#include <stdint.h>

struct nic_stats {
    uint64_t *rx_count;
    uint64_t *rx_size;

    uint64_t *tx_count;
    uint64_t *tx_size;
    
    uint64_t *remote_rx_count;
    uint64_t *remote_tx_count;

    uint64_t *rx_nombuf_count;
};

struct dataplane_stats {
    struct nic_stats nic_stats;
};

struct dp_config;

int
dataplane_globalstat_register_counters(struct dp_config *dp_config);

void *
stat_nic_thread(void* arg);