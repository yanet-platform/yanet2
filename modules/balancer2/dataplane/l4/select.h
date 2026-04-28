#pragma once

#include <stdint.h>

struct worker_context;
struct session;
struct packet_context;
struct balancer_real;
struct balancer_session_table_chain;

/*
 * Select the real server that should handle this packet.
 *
 * In OPS (one-packet-scheduling) mode, each packet is independently
 * assigned to a real via the selector ring. No session is created
 * or consulted.
 *
 * Otherwise, a session slot is acquired from the session table via
 * st_get_or_create_session, which either finds an existing entry or
 * allocates a new one. In both cases a locked pointer to the
 * session_state is returned. The slot is used as follows:
 *
 * - Session found, real is valid and enabled:
 *   The session is prolonged (timestamps and timeout updated)
 *   and the same real is returned.
 *
 * - Session found, but the real was disabled or removed:
 *   The session is stale. If the packet is reschedulable
 *   (TCP SYN or any UDP), a new real is selected from the ring
 *   and the session slot is overwritten with the new real.
 *   Non-reschedulable packets (TCP non-SYN) are dropped and
 *   the session slot is freed.
 *
 * - No session found (new flow):
 *   A blank slot is allocated. If the packet is reschedulable,
 *   a new real is selected and written into the slot.
 *   Non-reschedulable packets are dropped and the slot is
 *   freed -- this handles stray TCP ACKs arriving after
 *   session timeout.
 *
 * The session lock is always released before returning.
 *
 * Returns the selected real, or NULL if the packet should be
 * dropped. The caller is responsible for dropping NULL-result
 * packets.
 */
struct real *
select_real(
	struct worker_context *context,
	struct packet_context *pkt_ctx,
	struct session *session,
	struct balancer_session_table_chain *st_chain,
	uint64_t st_chain_gen
);
