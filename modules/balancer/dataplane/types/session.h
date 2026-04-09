#pragma once

#include "common/network.h"
#include "common/rcu.h"
#include "common/ttlmap/detail/ttlmap.h"

#define BALANCER_SESSION_ID_PADDING                                            \
	(32 - (sizeof(uint64_t) + sizeof(uint16_t) + NET6_LEN))

struct balancer_session_id {
	uint64_t vs_stable_idx;
	uint16_t client_port;
	uint8_t client_ip[NET6_LEN];
	uint8_t padding[BALANCER_SESSION_ID_PADDING];
};

struct balancer_session_state {
	/*
	 * stable_idx of the assigned real; see struct balancer_vs.
	 */
	uint64_t real_stable_idx;
	uint32_t last_packet_timestamp;
	uint32_t create_timestamp;
	uint8_t timeout;
};

struct balancer_session_timeouts {
	uint8_t tcp_syn_ack;
	uint8_t tcp_syn;
	uint8_t tcp_fin;
	uint8_t tcp;
	uint8_t udp;
};

/*
 * Double-buffered session table with RCU-based resizing.
 *
 * The table contains two ttlmaps. At any given time, one is the
 * "current" map (where workers insert new sessions) and the other
 * is the "previous" map (which may still hold sessions from before
 * a resize).
 *
 * The controlplane resizes the table by:
 *  1. Preparing the new map in the inactive slot.
 *  2. Incrementing current_gen (atomically).
 *  3. Waiting for all workers to observe the new generation (RCU).
 *  4. Incrementing current_gen again once migration is complete.
 *
 * Generation parity controls dual-map behavior:
 *  - Even generation (stable): workers use only the current map.
 *    The previous map is not consulted and may be rebuilt.
 *  - Odd generation (transition): workers write to the current map
 *    but also check the previous map for existing sessions that
 *    have not yet been migrated.
 *
 * This allows resizing without dropping active sessions: during
 * the transition (odd gen), a session not found in the new map
 * may still exist in the old one. Once all sessions have been
 * migrated or expired, the controlplane moves to the next even
 * generation, and the old map is freed and can be reused.
 */
struct balancer_session_table {
	struct ttlmap maps[2];
	rcu_t rcu;
	_Atomic uint64_t current_gen;
	struct memory_context mctx;
	uint32_t workers;
};

static inline int
balancer_st_map_idx(uint32_t gen) {
	return ((gen + 1) & 0b11) >> 1;
}

static inline struct ttlmap *
balancer_st_cur_map(struct balancer_session_table *table, uint32_t gen) {
	return &table->maps[balancer_st_map_idx(gen)];
}

static inline struct ttlmap *
balancer_st_prev_map(struct balancer_session_table *table, uint32_t gen) {
	return &table->maps[balancer_st_map_idx(gen) ^ 1];
}
