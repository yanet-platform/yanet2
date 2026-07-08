#include <netinet/in.h>
#include <stdatomic.h>
#include <string.h>

#include <rte_mbuf.h>
#include <rte_tcp.h>

#include "common/big_array.h"
#include "common/flatmap.h"
#include "common/memory_address.h"
#include "common/network.h"
#include "common/ttlmap/detail/lock.h"

#include "lib/dataplane/packet/packet.h"

#include "../session.h"
#include "context.h"
#include "packet.h"
#include "real.h"
#include "selector.h"
#include "session.h"
#include "vs.h"

#include "types/stats.h"

#define INVALID_REAL_IDX ((uint32_t)-1)

static uint32_t
ring_get(struct ring *ring, uint64_t index) {
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
	struct real_selector *selector, uint32_t worker, uint32_t hash
) {
	size_t ring_id =
		atomic_load_explicit(&selector->ring_id, memory_order_acquire);

	struct ring *ring = &selector->rings[ring_id];

	uint64_t idx = (selector->workers_rr_counter[worker].value++ &
			~selector->packet_hash_mask) |
		       (hash & selector->packet_hash_mask);
	return ring_get(ring, idx);
}

static void
update_session_state(
	struct balancer_session_state *session_state,
	uint32_t now,
	uint32_t session_timeout
) {
	session_state->last_packet_timestamp = now;
	session_state->timeout = session_timeout;
}

static void
create_session_state(
	struct balancer_session_state *session_state,
	struct real *real,
	uint32_t now,
	uint32_t session_timeout
) {
	session_state->create_timestamp = now;
	session_state->last_packet_timestamp = now;
	session_state->real_ip.addr = real->addr;
	session_state->real_ip.family =
		(real_flags(real) & real_ip6 ? ip_family_ip6 : ip_family_ip4);
	session_state->timeout = session_timeout;
}

static struct real *
vs_lookup_real(struct virtual_service *vs, struct real_ip *real_ip) {
	void *id = flatmap_lookup(
		&vs->reals_map, real_ip, sizeof(*real_ip), sizeof(uint32_t)
	);
	return id == NULL ? NULL : ADDR_OF(&vs->reals) + *(uint32_t *)id;
}

/*
 * Checks whether a real is currently enabled, accounting the packet
 * against it if not.
 *
 * A real selected from the ring can still be disabled: ring rebuilds
 * and the real's enabled flag are updated as separate steps by the
 * control plane, so a worker can briefly observe a stale ring entry
 * for a real that was just disabled.
 */
static bool
real_is_usable(
	struct worker_context *context,
	struct real *real,
	struct balancer_vs_stats *vs_stats
) {
	if (likely(real_flags(real) & real_enabled)) {
		return true;
	}
	struct balancer_real_stats *stats =
		real_fetch_stats(real, context->counter_storage);
	stats->packets_real_disabled += 1;
	vs_stats->real_is_disabled += 1;
	return false;
}

/*
 * Try to reuse the real from an existing session.
 * Returns the real if still exists and enabled, NULL otherwise.
 */
static struct real *
try_reuse_session_real(
	struct worker_context *context,
	struct packet_context *pkt_ctx,
	struct balancer_session_state *session_state
) {
	struct virtual_service *vs = pkt_ctx->matched_vs;
	struct balancer_vs_stats *vs_stats = pkt_ctx->matched_vs_stats;

	struct real *real = vs_lookup_real(vs, &session_state->real_ip);
	if (unlikely(real == NULL)) {
		return NULL;
	}

	if (unlikely(!real_is_usable(context, real, vs_stats))) {
		return NULL;
	}

	return real;
}

static struct real *
select_real_ops(
	struct worker_context *context,
	struct packet_context *pkt_ctx,
	struct virtual_service *vs
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
	struct real *real = ADDR_OF(&vs->reals) + real_idx;
	if (unlikely(!real_is_usable(context, real, pkt_ctx->matched_vs_stats)
	    )) {
		return NULL;
	}
	return real;
}

/*
 * Select a new real from the ring and write it into the session slot.
 * Returns the real, or NULL if the ring is empty or the selected real
 * is disabled.
 */
static struct real *
schedule_new_real(
	struct worker_context *context,
	struct packet_context *pkt_ctx,
	struct session *session,
	struct virtual_service *vs,
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

	struct real *real = ADDR_OF(&vs->reals) + real_id;
	if (unlikely(!real_is_usable(context, real, pkt_ctx->matched_vs_stats)
	    )) {
		return NULL;
	}
	create_session_state(
		session_state, real, context->now, session->timeout
	);

	struct balancer_real_stats *real_stats =
		real_fetch_stats(real, context->counter_storage);
	real_stats->created_sessions += 1;
	pkt_ctx->matched_vs_stats->created_sessions += 1;

	return real;
}

struct real *
select_real(
	struct worker_context *context,
	struct packet_context *pkt_ctx,
	struct session *session,
	struct balancer_session_table_chain *st_chain,
	uint64_t current_table_gen
) {
	struct virtual_service *vs = pkt_ctx->matched_vs;
	struct balancer_vs_stats *vs_stats = pkt_ctx->matched_vs_stats;

	/* OPS: stateless selection, no session involved. */
	if (vs->flags & vs_ops) {
		return select_real_ops(context, pkt_ctx, vs);
	}

	/*
	 * Acquire a session slot from the session table. The slot is either
	 * an existing entry or a freshly allocated one, returned under a lock
	 * that must be released before this function returns.
	 *
	 * Possible results:
	 * - SESSION_FOUND:    existing entry, session_state is populated.
	 * - SESSION_CREATED:  new (or recycled) entry, session_state is blank.
	 * - SESSION_TABLE_OVERFLOW: table is full, no slot acquired, no lock
	 * held.
	 */
	struct balancer_session_state *session_state = NULL;
	ttlmap_lock_t *session_lock;
	int result = st_chain_get_or_create_session(
		st_chain,
		current_table_gen,
		context->now,
		session->timeout,
		&session->id,
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
		struct real *real =
			try_reuse_session_real(context, pkt_ctx, session_state);
		if (real != NULL) {
			/* Real is still valid -- prolong the session. */
			update_session_state(
				session_state, context->now, session->timeout
			);
			st_chain_unlock_session(session_lock);
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
	if (unlikely(!session->can_reschedule)) {
		vs_stats->not_rescheduled_packets += 1;
		st_chain_remove_session(session_state);
		st_chain_unlock_session(session_lock);
		return NULL;
	}

	/* Select a new real and write it into the session slot. */
	struct real *real =
		schedule_new_real(context, pkt_ctx, session, vs, session_state);
	if (unlikely(real == NULL)) {
		st_chain_remove_session(session_state);
	}

	st_chain_unlock_session(session_lock);

	return real;
}