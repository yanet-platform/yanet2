#pragma once

#include "common/memory_address.h"

#include "handler/map.h"
#include "handler/vs.h"
#include "rte_tcp.h"
#include "selector.h"
#include "session_table.h"
#include <assert.h>
#include <filter/filter.h>
#include <netinet/in.h>
#include <stdint.h>

#include "../flow/common.h"
#include "../flow/context.h"
#include "../flow/helpers.h"

#include "state/session.h"
#include "state/session_table.h"

#include "api/vs.h"

////////////////////////////////////////////////////////////////////////////////

static inline bool
reschedule_real(uint8_t transport_proto, uint16_t tcp_flags) {
	// True for UDP and TCP SYN packets
	return (transport_proto == IPPROTO_UDP) ||
	       (transport_proto == IPPROTO_TCP &&
		((tcp_flags & (RTE_TCP_SYN_FLAG | RTE_TCP_RST_FLAG)) ==
		 RTE_TCP_SYN_FLAG));
}

// Selects real and update real and virtual service stats.
static inline struct real *
select_real(
	struct packet_ctx *ctx,
	struct vs *vs,
	struct session_table *table,
	uint64_t current_table_gen
) {
	struct packet_handler *handler = ctx->handler;
	struct real *reals = ADDR_OF(&handler->reals);

	struct map *reals_index = &handler->reals_index;

	const size_t worker_idx = ctx->worker->idx;
	const uint32_t now = ctx->now;

	// if `One Packet Scheduling` flag is set,
	// we do not account for sessions
	if (vs->flags & VS_OPS_FLAG) {
		uint32_t local_real_id = selector_select(
			&vs->selector, worker_idx, ctx->packet->hash
		);
		if (local_real_id == SELECTOR_VALUE_INVALID) {
			// discard packet because there are no enabled reals

			// update counter
			VS_STATS_INC(no_reals, ctx);

			return NULL;
		}

		uint32_t real_id = vs->first_real_idx + local_real_id;

		// select real
		struct real *real = &reals[real_id];
		packet_ctx_set_real(ctx, real);

		// update stats

		// real stats
		REAL_STATS_INC(ops_packets, ctx);

		// vs stats
		VS_STATS_INC(ops_packets, ctx);

		return real;
	}

	// get state for the session
	struct session_state *session_state = NULL;
	session_lock_t *session_lock;
	int get_session_result = get_or_create_session(
		table,
		current_table_gen,
		now,
		ctx->session_timeout,
		&ctx->session,
		&session_state,
		&session_lock
	);

	if (get_session_result ==
	    SESSION_TABLE_OVERFLOW) { // session with such id is not present and
				      // there is no enough space in the session
				      // table to create new state, so error
		// update virtual service stats
		VS_STATS_INC(session_table_overflow, ctx);

		return NULL;
	}

	if (get_session_result == SESSION_FOUND) { // session with such id found
		// session_state->real_id contains the global registry index
		size_t real_stable_idx = session_state->real_id;
		uint64_t real_ph_idx;
		int find_res =
			map_find(reals_index, real_stable_idx, &real_ph_idx);

		if (find_res == -1) {
			// session is for real which is not
			// configured for the current packet handler.

			// increase stats, then try reschedule packet to the
			// other real
			VS_STATS_INC(real_is_removed, ctx);
		} else if (!vs_real_enabled(
				   ctx->vs.ptr, real_ph_idx
			   )) { // check if real is
				// disabled
			// real is disabled

			struct real *real = &reals[real_ph_idx];

			// select real to update its counters
			packet_ctx_set_real(ctx, real);

			// increment stats
			REAL_STATS_INC(packets_real_disabled, ctx);
			VS_STATS_INC(real_is_disabled, ctx);

			// deselect real
			packet_ctx_unset_real(ctx);
		} else {
			// real enabled and present in config, so we select it.

			struct real *real = &reals[real_ph_idx];

			// set real in packet context
			packet_ctx_set_real(ctx, real);

			// update session and unlock it
			session_state->timeout = ctx->session_timeout;
			session_state->last_packet_timestamp = now;
			session_unlock(session_lock);

			// real is selected, just return it.
			return real;
		}
	}

	// session not found or real is disabled
	// but session inserted into table and
	// we have pointer to session state with acquired lock.

	// now we need to select real for packet

	assert(session_state != NULL);
	if (!reschedule_real(
		    ctx->transport_proto, ctx->tcp_flags
	    )) { // packet type not allows to create new session
		VS_STATS_INC(not_rescheduled_packets, ctx);
		session_remove(session_state); // free created state
		session_unlock(session_lock);  // unlock state
		return NULL;
	}

	// select new real for the session and remember it in session state

	uint32_t local_real_id =
		selector_select(&vs->selector, worker_idx, ctx->packet->hash);
	if (local_real_id == SELECTOR_VALUE_INVALID) {
		VS_STATS_INC(no_reals, ctx);
		session_remove(session_state); // free created state
		session_unlock(session_lock);  // unlock state
		return NULL;
	}

	uint32_t real_id = vs->first_real_idx + local_real_id;

	// real selected, new session is created

	// set real
	struct real *real = &reals[real_id];
	packet_ctx_set_real(ctx, real);

	session_state->create_timestamp = now;
	session_state->last_packet_timestamp = now;
	session_state->real_id = real->stable_idx;
	session_state->timeout = ctx->session_timeout;

	session_unlock(session_lock);

	// update stats
	VS_STATS_INC(created_sessions, ctx);
	REAL_STATS_INC(created_sessions, ctx);

	return real;
}