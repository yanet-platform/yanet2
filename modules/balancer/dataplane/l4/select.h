#pragma once

#include "common/memory_address.h"

#include "../meta.h"

#include "modules/balancer/state/state.h"
#include "rte_tcp.h"
#include <assert.h>
#include <filter/filter.h>
#include <netinet/in.h>
#include <stdint.h>

#include "../real.h"
#include "../vs.h"

#include "../flow/common.h"
#include "../flow/context.h"
#include "../flow/helpers.h"
#include "../flow/stats.h"

#include "../../state/session.h"
#include "../../state/session_table.h"

#include "../../api/vs.h"

////////////////////////////////////////////////////////////////////////////////

static inline bool
reschedule_real(struct packet_metadata *metadata) {
	// True for UDP and TCP SYN packets
	return (metadata->transport_proto == IPPROTO_UDP) ||
	       (metadata->transport_proto == IPPROTO_TCP &&
		((metadata->tcp_flags & (RTE_TCP_SYN_FLAG | RTE_TCP_RST_FLAG)
		 ) == RTE_TCP_SYN_FLAG));
}

////////////////////////////////////////////////////////////////////////////////

static inline uint32_t
next_rnd(
	struct virtual_service *vs, struct packet_metadata *meta, size_t worker
) {
	return vs->flags & BALANCER_VS_PRR_FLAG
		       ? vs->worker_local[worker].round_robin_counter++
		       : meta->hash;
}

// Selects real and update real and virtual service stats.
static inline struct real *
select_real(
	struct packet_ctx *ctx,
	struct balancer_module_config *config,
	uint32_t now,
	uint32_t worker_idx,
	struct virtual_service *vs,
	struct packet_metadata *metadata
) {
	struct balancer_state *balancer_state = ADDR_OF(&config->state);
	struct real *reals = ADDR_OF(&config->reals);

	// if `One Packet Scheduling` flag is set,
	// we do not account for sessions
	if (vs->flags & BALANCER_VS_OPS_FLAG) {
		uint32_t real_id = ring_get(
			&vs->real_ring, next_rnd(vs, metadata, ctx->worker->idx)
		);
		if (real_id == RING_VALUE_INVALID) {
			// discard packet because there are no enabled reals

			// update counter
			VS_STATS_INC(no_reals, ctx);

			return NULL;
		}

		// select real
		struct real *real = &reals[real_id];
		packet_ctx_set_real(ctx, real);

		// update stats

		// real stats
		packet_ctx_update_real_stats_on_packet(ctx);
		REAL_STATS_INC(ops_packets, ctx);

		// vs stats
		packet_ctx_update_vs_stats_on_outgoing_packet(ctx);
		VS_STATS_INC(ops_packets, ctx);

		return real;
	}

	// get timeout for the session based on transport protocol flags
	uint32_t timeout =
		session_timeout(&config->sessions_timeouts, metadata);

	// setup id for the session between client and virtual service
	struct balancer_session_id session_id;
	fill_session_id(&session_id, metadata, vs);

	// begin critical section
	struct session_table *table = &balancer_state->session_table;
	uint64_t current_table_gen = session_table_begin_cs(table, worker_idx);

	// get state for the session
	struct balancer_session_state *session_state = NULL;
	session_lock_t *session_lock;
	int get_session_result = get_or_create_session(
		table,
		current_table_gen,
		now,
		timeout,
		&session_id,
		&session_state,
		&session_lock
	);

	if (get_session_result ==
	    SESSION_TABLE_OVERFLOW) { // session with such id is not present and
				      // there is no enough space in the session
				      // table to create new state, so error
		// update virtual service stats
		VS_STATS_INC(session_table_overflow, ctx);

		// end critical section
		session_table_end_cs(table, worker_idx);

		return NULL;
	}

	if (get_session_result == SESSION_FOUND) { // session with such id found
		struct real *real = &reals[session_state->real_id];

		// first, check real flags
		// to determine case when real disabled or not in the
		// current config. in that case, we just skip current real
		// and try to reschedule packet on the other one.
		if (!(real->flags & REAL_PRESENT_IN_CONFIG_FLAG)) {
			// real is not present in current config
			// deselect real
			packet_ctx_unset_real(ctx);
		} else if (real->flags & BALANCER_REAL_DISABLED_FLAG) {
			// real is disabled

			// select real to update its counters
			packet_ctx_set_real(ctx, real);

			REAL_STATS_INC(packets_real_disabled, ctx);

			// deselect real
			packet_ctx_unset_real(ctx);
		} else {
			// real enabled and present in config, so we select it.
			// calculate until session was encountered

			// set real in packet context
			packet_ctx_set_real(ctx, real);

			// update session and unlock it
			session_state->timeout = timeout;
			session_state->last_packet_timestamp = now;
			session_unlock(session_lock);

			// update real and virtual service stats
			packet_ctx_update_real_stats_on_packet(ctx);
			packet_ctx_update_vs_stats_on_outgoing_packet(ctx);

			// end critical section
			session_table_end_cs(table, worker_idx);

			// real is selected, just return it.
			return real;
		}
	}

	// session not found or real is disabled
	// but session inserted into table and
	// we have pointer to session state with acquired lock.

	// now we need to select real for packet

	assert(session_state != NULL);
	if (!reschedule_real(metadata
	    )) { // packet type not allows to create new session
		VS_STATS_INC(not_rescheduled_packets, ctx);
		session_remove(session_state); // free created state
		session_unlock(session_lock);  // unlock state

		// end critical section
		session_table_end_cs(table, worker_idx);
		return NULL;
	}

	// select new real for the session and remember it in session state

	uint32_t real_id = ring_get(
		&vs->real_ring, next_rnd(vs, metadata, ctx->worker->idx)
	);
	if (real_id == RING_VALUE_INVALID) {
		VS_STATS_INC(no_reals, ctx);
		session_remove(session_state); // free created state
		session_unlock(session_lock);  // nlock state
		session_table_end_cs(table, worker_idx);
		return NULL;
	}

	// real selected, new session is created

	// set real
	struct real *real = &reals[real_id];
	packet_ctx_set_real(ctx, real);

	session_state->create_timestamp = now;
	session_state->last_packet_timestamp = now;
	session_state->real_id = real_id;
	session_state->timeout = timeout;

	session_unlock(session_lock);

	// end critical section
	session_table_end_cs(table, worker_idx);

	// update stats
	packet_ctx_update_vs_stats_on_outgoing_packet(ctx);
	VS_STATS_INC(created_sessions, ctx);

	packet_ctx_update_real_stats_on_packet(ctx);
	REAL_STATS_INC(created_sessions, ctx);

	return real;
}