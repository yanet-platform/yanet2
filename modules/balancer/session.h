#pragma once

#include <stdint.h>

#include "common/detail/ttlmap/lock.h"

////////////////////////////////////////////////////////////////////////////////

struct balancer_session_id {
    uint8_t protocol;
	uint8_t l3_balancing;
	uint8_t addr_type; // 4=ip4, 6=ip6.

	uint8_t ip_source[16];
	uint8_t ip_destination[16];

	uint16_t port_source;
	uint16_t port_destination;
};

struct balancer_session_state {
    uint32_t real_id; // global id of real
	uint32_t create_timestamp;
	uint32_t last_packet_timestamp;
	uint32_t timeout;
};

typedef ttlmap_lock_t balancer_session_lock_t;

struct balancer_sessions_storage_gen {
    __rte_cache_aligned struct ttlmap session_table;
    __rte_cache_aligned struct balancer_state_worker_local worker_local[MAX_WORKERS_NUM];
    size_t table_capacity;
};