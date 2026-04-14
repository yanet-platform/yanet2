#include <netinet/in.h>
#include <string.h>

#include <rte_mbuf.h>
#include <rte_tcp.h>

#include "common/big_array.h"
#include "common/memory_address.h"

#include "common/ttlmap/detail/lock.h"
#include "lib/dataplane/packet/packet.h"

#include "context.h"
#include "packet.h"
#include "real_helpers.h"
#include "rte_branch_prediction.h"
#include "session/table.h"
#include "session/tracker.h"
#include "types/selector.h"
#include "types/vs.h"

#define INVALID_REAL_IDX ((uint32_t)-1)

static uint32_t
ring_get(struct balancer_ring *ring, uint64_t index) {
	if (ring->real_ids.size > 0) {
		uint32_t pos = index % (ring->real_ids.size / sizeof(uint32_t));
		uint32_t val;
		memcpy(&val,
		       big_array_get(&ring->real_ids, pos * sizeof(uint32_t)),
		       sizeof(val));
		return val;
	} else {
		return INVALID_REAL_IDX;
	}
}

static uint32_t
selector_select(
	struct balancer_real_selector *selector, uint32_t worker, uint32_t hash
) {
	size_t ring_id =
		atomic_load_explicit(&selector->ring_id, memory_order_acquire);
	struct balancer_ring *ring = &selector->rings[ring_id];

	/*
	 * Here branch predictor works well,
	 * because use_rr is long-term variable.
	 * So, we dont use ternary operator here.
	 */
	uint64_t idx;
	if (selector->use_rr) {
		idx = selector->workers[worker].value++;
	} else {
		idx = hash;
	}

	return ring_get(ring, idx);
}

static void
update_session_state(
	struct balancer_session_state *session_state,
	struct balancer_real *real,
	uint32_t worker_idx,
	uint32_t now,
	uint8_t session_timeout
) {
	sessions_tracker_prolong_session(
		ADDR_OF(&real->tracker_shards),
		worker_idx,
		session_state->last_packet_timestamp,
		session_state->timeout,
		now,
		session_timeout
	);
	session_state->last_packet_timestamp = now;
	session_state->timeout = session_timeout;
}

static void
create_session_state(
	struct balancer_session_state *session_state,
	struct balancer_real *real,
	uint32_t worker_idx,
	uint32_t now,
	uint8_t session_timeout
) {
	sessions_tracker_new_session(
		ADDR_OF(&real->tracker_shards), worker_idx, now, session_timeout
	);
	session_state->create_timestamp = now;
	session_state->last_packet_timestamp = now;
	session_state->real_stable_idx = real->stable_idx;
	session_state->timeout = session_timeout;
}

/*
 * Try to reuse the real from an existing session.
 * Returns the real if still valid and enabled, NULL otherwise.
 */
static struct balancer_real *
try_reuse_session_real(
	struct worker_context *context,
	struct l4_packet_context *pkt_ctx,
	struct balancer_session_state *session_state
) {
	struct balancer_vs *vs = pkt_ctx->matched_vs;
	struct balancer_vs_stats *vs_stats = pkt_ctx->matched_vs_stats;

	uint32_t real_idx =
		real_idx_from_stable_idx(session_state->real_stable_idx);
	struct balancer_real *real = ADDR_OF(&vs->reals) + real_idx;

	/*
	 * Slot was reused for a different real since the session was created
	 * or the real is removed without reuse.
	 */
	if (unlikely(
		    session_state->real_stable_idx != real->stable_idx ||
		    real_is_removed(real)
	    )) {
		vs_stats->real_is_removed += 1;
		return NULL;
	}

	if (unlikely(!real_is_enabled(real))) {
		struct balancer_real_stats *real_stats = real_get_stats(
			real, context->worker_idx, context->counter_storage
		);
		real_stats->packets_real_disabled += 1;
		vs_stats->real_is_disabled += 1;
		return NULL;
	}

	return real;
}

static struct balancer_real *
resolve_real_ops(
	struct worker_context *context,
	struct l4_packet_context *pkt_ctx,
	struct balancer_vs *vs
) {
	uint32_t real_idx = selector_select(
		ADDR_OF(&vs->selector),
		context->worker_idx,
		pkt_ctx->packet->hash
	);
	if (unlikely(real_idx == INVALID_REAL_IDX)) {
		pkt_ctx->matched_vs_stats->no_reals += 1;
		return NULL;
	}
	return ADDR_OF(&vs->reals) + real_idx;
}

/*
 * Select a new real from the ring and write it into the session slot.
 * Returns the real, or NULL if the ring is empty.
 */
static struct balancer_real *
schedule_new_real(
	struct worker_context *context,
	struct l4_packet_context *pkt_ctx,
	struct balancer_vs *vs,
	struct balancer_session_state *session_state
) {
	uint32_t real_id = selector_select(
		ADDR_OF(&vs->selector),
		context->worker_idx,
		pkt_ctx->packet->hash
	);
	if (unlikely(real_id == INVALID_REAL_IDX)) {
		pkt_ctx->matched_vs_stats->no_reals += 1;
		return NULL;
	}

	struct balancer_real *real = ADDR_OF(&vs->reals) + real_id;
	create_session_state(
		session_state,
		real,
		context->worker_idx,
		context->now,
		pkt_ctx->session_timeout
	);

	struct balancer_real_stats *real_stats = real_get_stats(
		real, context->worker_idx, context->counter_storage
	);
	real_stats->created_sessions += 1;
	pkt_ctx->matched_vs_stats->created_sessions += 1;

	return real;
}

struct balancer_real *
resolve_real(
	struct worker_context *context,
	struct l4_packet_context *pkt_ctx,
	struct balancer_session_table *st,
	uint64_t current_table_gen
) {
	struct balancer_vs *vs = pkt_ctx->matched_vs;
	struct balancer_vs_stats *vs_stats = pkt_ctx->matched_vs_stats;

	/* OPS: stateless selection, no session involved. */
	if (vs->flags & balancer_vs_ops) {
		return resolve_real_ops(context, pkt_ctx, vs);
	}

	/*
	 * Acquire a session slot from the session table.
	 *
	 * st_get_or_create_session either finds an existing entry
	 * or allocates a new one. In both cases it returns a locked
	 * pointer to the session_state. The caller must eventually
	 * call st_unlock_session to release the lock.
	 *
	 * Possible results:
	 * - SESSION_FOUND:    existing entry, session_state is populated.
	 * - SESSION_CREATED:  new (or recycled) entry, session_state is blank.
	 * - SESSION_TABLE_OVERFLOW: table is full, no slot acquired, no lock
	 * held.
	 */
	struct balancer_session_state *session_state = NULL;
	ttlmap_lock_t *session_lock;
	int result = st_get_or_create_session(
		st,
		current_table_gen,
		context->now,
		pkt_ctx->session_timeout,
		&pkt_ctx->session_id,
		&session_state,
		&session_lock
	);

	if (unlikely(result == SESSION_TABLE_OVERFLOW)) {
		vs_stats->session_table_overflow += 1;
		return NULL;
	}

	/*
	 * From here on, session_lock is held and must be released
	 * before returning. session_state points to the locked slot.
	 */

	/* Try to reuse the real from an existing session. */
	if (result == SESSION_FOUND) {
		struct balancer_real *real =
			try_reuse_session_real(context, pkt_ctx, session_state);
		if (real != NULL) {
			/* Real is still valid -- prolong the session. */
			update_session_state(
				session_state,
				real,
				context->worker_idx,
				context->now,
				pkt_ctx->session_timeout
			);
			st_unlock_session(session_lock);
			return real;
		}
		/*
		 * Real is gone (disabled or removed). The session slot
		 * is still allocated and locked -- fall through to
		 * reschedule, which will overwrite it with a new real.
		 */
	}

	/*
	 * At this point we need to assign a new real to the session slot.
	 * This happens when:
	 * - SESSION_CREATED: no prior session existed (new flow).
	 * - SESSION_FOUND but real is stale (disabled/removed).
	 *
	 * Only connection-initiating packets may create or overwrite
	 * sessions. Non-initiating packets (TCP non-SYN) are dropped
	 * and the session slot is freed.
	 */
	if (unlikely(!pkt_ctx->can_reschedule)) {
		vs_stats->not_rescheduled_packets += 1;
		st_remove_session(session_state);
		st_unlock_session(session_lock);
		return NULL;
	}

	/* Select a new real and write it into the session slot. */
	struct balancer_real *real =
		schedule_new_real(context, pkt_ctx, vs, session_state);
	if (unlikely(real == NULL)) {
		st_remove_session(session_state);
	}

	st_unlock_session(session_lock);

	return real;
}