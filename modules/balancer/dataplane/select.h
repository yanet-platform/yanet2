#pragma once

#include "common/memory_address.h"
#include "meta.h"
#include "modules/balancer/state/registry.h"
#include "rte_tcp.h"
#include <assert.h>
#include <filter/filter.h>
#include <netinet/in.h>
#include <stdint.h>

#include "real.h"
#include "vs.h"

#include "../state/session.h"
#include "../state/session_table.h"

#include "../api/vs.h"

////////////////////////////////////////////////////////////////////////////////

static inline void
put_session(
	struct real *real,
	struct virtual_service *vs,
	size_t worker,
	uint32_t now,
	uint32_t from,
	uint32_t timeout
) {
	// update for real
	struct service_state *real_state = ADDR_OF(&real->state) + worker;
	service_state_put_session(real_state, now, from, timeout);
	service_state_update(real_state, now);

	// update for vs
	struct service_state *vs_state = ADDR_OF(&vs->state) + worker;
	service_state_put_session(vs_state, now, from, timeout);
	service_state_update(vs_state, now);
}

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
next_rnd(struct virtual_service *vs, struct packet_metadata *meta) {
	return vs->flags & BALANCER_VS_PRR_FLAG ? vs->round_robin_counter++
						: meta->hash;
}

static inline struct real *
select_real(
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
		uint32_t real_id =
			ring_get(&vs->real_ring, next_rnd(vs, metadata));
		if (real_id == RING_VALUE_INVALID) {
			return NULL;
		}
		return &reals[real_id];
	}

	// get timeout for the session based on transport protocol flags
	uint32_t timeout = session_timeout(&balancer_state->timeouts, metadata);

	// setup id for the session between client and virtual service
	struct session_id session_id;
	fill_session_id(
		&session_id, metadata, vs->flags & BALANCER_VS_PURE_L3_FLAG
	);

	// get state for the session
	struct session_state *session_state = NULL;
	session_lock_t *session_lock;
	int get_session_result = get_or_create_session(
		&balancer_state->session_table,
		worker_idx,
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
		return NULL;
	}

	if (get_session_result == SESSION_FOUND) { // session with such id found
		struct real *real = &reals[session_state->real_id];
		assert(real->weight > 0);

		// calculate until session was encountered
		uint32_t until = session_state->last_packet_timestamp +
				 session_state->timeout;
		uint32_t time_from = now > until ? now : until;

		// update session and unlock it
		session_state->timeout = timeout;
		session_state->last_packet_timestamp = now;
		session_unlock(session_lock);

		// put prolonged session into state
		put_session(real, vs, worker_idx, now, time_from, timeout);
		return real;
	}

	// session with such id not found, but table inserted this session and
	// returned pointer to session state with acquired lock.

	// so, here session is created

	assert(session_state != NULL);
	if (!reschedule_real(metadata
	    )) { // packet type not allows to create new session
		session_remove(session_state); // free created state
		session_unlock(session_lock);  // unlock state
		return NULL;
	}

	// select new real for the session and remember it in session state

	uint32_t real_id = ring_get(&vs->real_ring, next_rnd(vs, metadata));
	if (real_id == RING_VALUE_INVALID) {
		session_unlock(session_lock);
		return NULL;
	}
	session_state->create_timestamp = now;
	session_state->last_packet_timestamp = now;
	session_state->real_id = real_id;
	session_state->timeout = timeout;

	session_unlock(session_lock);

	struct real *real = &reals[real_id];

	// put session into state
	put_session(real, vs, worker_idx, now, now, timeout);

	return real;
}