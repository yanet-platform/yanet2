#pragma once

#include "common/memory_address.h"
#include "meta.h"
#include "ring.h"
#include "rte_tcp.h"
#include "session.h"
#include <assert.h>
#include <filter/filter.h>
#include <netinet/in.h>
#include <sched.h>
#include <stdint.h>

#include "real.h"
#include "session.h"
#include "session_table.h"
#include "vs.h"

#include "../api/vs.h"

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
	struct real *reals = ADDR_OF(&config->reals);

	// if `One Packet Scheduling` flag is set,
	// we do not account for sessions
	if (vs->flags & BALANCER_VS_OPS_FLAG) {
		uint32_t real_id =
			ring_get(&vs->real_ring, next_rnd(vs, metadata));
		if (real_id == RING_VALUE_INVALID) {
			return NULL;
		}
		real_id += vs->real_start;
		return &reals[real_id];
	}

	// get timeout for the session based on transport protocol flags
	uint32_t timeout = session_timeout(&config->timeouts, metadata);

	// setup id for the session between client and virtual service
	struct session_id session_id;
	fill_session_id(
		&session_id, metadata, vs->flags & BALANCER_VS_PURE_L3_FLAG
	);

	// get state for the session
	struct session_state *session_state = NULL;
	session_lock_t *session_lock;
	int get_session_result = get_or_create_session(
		ADDR_OF(&config->session_table),
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
		session_state->timeout = timeout;
		session_state->last_packet_timestamp = now;
		session_unlock(session_lock);
		return real;
	}

	// session with such id not found, but table inserted this session and
	// returned pointer to session state with acquired lock.

	assert(session_state != NULL);
	if (!reschedule_real(metadata
	    )) { // packet type not allows to create new session
		session_invalidate(session_state); // free created state
		session_unlock(session_lock);	   // unlock state
		return NULL;
	}

	// select new real for the session and remember it in session state

	uint32_t real_id = ring_get(&vs->real_ring, next_rnd(vs, metadata));
	if (real_id == RING_VALUE_INVALID) {
		session_unlock(session_lock);
		return NULL;
	}
	real_id += vs->real_start;
	session_state->create_timestamp = now;
	session_state->last_packet_timestamp = now;
	session_state->real_id = real_id;
	session_state->timeout = timeout;
	session_unlock(session_lock);
	return &reals[real_id];
}