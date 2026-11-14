#pragma once

#include <assert.h>
#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>
#include <string.h>

#include "nat64state.h"
#include "types.h"

// Install fwstate's fwmap callbacks into the statemap registry; idempotent,
// and run by every fwstate table creation path (see registry.c).
void
fwstate_fwmap_registry_ensure(void);

/*
 * NOTE: the `size` argument of every callback below is currently ignored
 * (the hard-cast makes it redundant). A temporary assert is kept as a
 * correctness net until Phase 2 removes the `size` parameter entirely.
 * See https://github.com/yanet-platform/yanet2/issues/1534.
 */

// === Custom copy functions for fwmap keys and values.

static inline void
fwmap_copy_key_fw4(void *dst, const void *src, size_t size) {
	assert(size == sizeof(struct fw4_state_key));
	const struct fw4_state_key *s = (const struct fw4_state_key *)src;
	struct fw4_state_key *d = (struct fw4_state_key *)dst;

	*d = *s;
}

static inline void
fwmap_copy_key_fw6(void *dst, const void *src, size_t size) {
	assert(size == sizeof(struct fw6_state_key));
	const struct fw6_state_key *s = (const struct fw6_state_key *)src;
	struct fw6_state_key *d = (struct fw6_state_key *)dst;

	*d = *s;
}

// In-layer update for an fwstate entry.
//
// When `dst_empty` is true this is a fresh insert and we simply copy *src.
// Otherwise *dst already holds the previous value of the same key in the same
// active layer, and we must accumulate state across the update so that no
// information is lost between consecutive sync frames.
static inline void
fwmap_update_value_fwstate(
	void *dst, const void *src, bool dst_empty, size_t size
) {
	assert(size == sizeof(struct fw_state_value));
	const struct fw_state_value *s = (const struct fw_state_value *)src;
	struct fw_state_value *d = (struct fw_state_value *)dst;

	if (dst_empty) {
		*d = *s;
		return;
	}

	// Snapshot the previous value before we overwrite *dst with *src.
	struct fw_state_value old = *d;
	*d = *s;

	// Update: take ownership/timestamp/last_ttl from the incoming frame,
	// but keep the original creation timestamp so the connection age is
	// preserved.
	d->created_at = old.created_at;
	// (external, updated_at, and last_ttl are inherited from *s by *d =
	// *s.)

	// Merge: TCP flags accumulate across frames (FIN/SYN/RST/ACK once seen,
	// stays seen), and per-direction packet counters are summed.
	d->flags.raw |= old.flags.raw;
	d->packets_forward += old.packets_forward;
	d->packets_backward += old.packets_backward;
}

// Cross-layer promotion of an fwstate entry from a stale (lower) layer into
// a freshly allocated slot in the active layer.
//
// *dst points to the new empty slot. *new_value is the incoming value being
// inserted into the active layer. *old_value is the existing value located in
// a stale layer below. We combine both:
//   - created_at is taken from the oldest copy (the stale one);
//   - external and updated_at are taken from the incoming value;
//   - TCP flags are OR-merged;
//   - per-direction packet counters are summed.
static inline void
fwmap_promote_value_fwstate(
	void *dst, const void *new_value, const void *old_value, size_t size
) {
	assert(size == sizeof(struct fw_state_value));

	struct fw_state_value *d = (struct fw_state_value *)dst;
	const struct fw_state_value *new_v =
		(const struct fw_state_value *)new_value;
	const struct fw_state_value *old_v =
		(const struct fw_state_value *)old_value;

	// Update: ownership, last-seen timestamp, and last_ttl come from the
	// incoming frame; the creation timestamp comes from the older copy in
	// the stale layer so the connection age is preserved across promotion.
	d->external = new_v->external;
	d->updated_at = new_v->updated_at;
	d->created_at = old_v->created_at;
	d->last_ttl = new_v->last_ttl;

	// Merge: TCP flags are OR-combined and per-direction packet counters
	// are summed across the two layers.
	d->flags.raw = new_v->flags.raw | old_v->flags.raw;
	d->packets_backward = new_v->packets_backward + old_v->packets_backward;
	d->packets_forward = new_v->packets_forward + old_v->packets_forward;
}

// == Custom key comparison functions for fwstate keys.

static inline bool
fwmap_fw4_key_equal(const void *a, const void *b, size_t size) {
	assert(size == sizeof(struct fw4_state_key));
	const struct fw4_state_key *k1 = (const struct fw4_state_key *)a;
	const struct fw4_state_key *k2 = (const struct fw4_state_key *)b;

	return k1->hdr.proto == k2->hdr.proto &&
	       k1->hdr.src_port == k2->hdr.src_port &&
	       k1->hdr.dst_port == k2->hdr.dst_port &&
	       k1->src_addr == k2->src_addr && k1->dst_addr == k2->dst_addr;
}

static inline bool
fwmap_fw6_key_equal(const void *a, const void *b, size_t size) {
	assert(size == sizeof(struct fw6_state_key));
	const struct fw6_state_key *k1 = (const struct fw6_state_key *)a;
	const struct fw6_state_key *k2 = (const struct fw6_state_key *)b;

	return k1->hdr.proto == k2->hdr.proto &&
	       k1->hdr.src_port == k2->hdr.src_port &&
	       k1->hdr.dst_port == k2->hdr.dst_port &&
	       memcmp(k1->src_addr, k2->src_addr, sizeof(k1->src_addr)) == 0 &&
	       memcmp(k1->dst_addr, k2->dst_addr, sizeof(k1->dst_addr)) == 0;
}

// == Value merge for the stateful NAT64 session tables.
//
// A session's identity (the pool allocation on the lan side, the original
// IPv6 source on the wan side) is chosen once at flow creation and must
// survive every later refresh: a refresh carries the mapping it read back
// from the table, but a racing first packet on another worker may have
// installed a different one — the older copy in the slot wins.

// In-layer update for a nat64state session entry.
static inline void
fwmap_update_value_nat64state(
	void *dst, const void *src, bool dst_empty, size_t size
) {
	assert(size == sizeof(struct nat64state_value));
	const struct nat64state_value *s = (const struct nat64state_value *)src;
	struct nat64state_value *d = (struct nat64state_value *)dst;

	if (dst_empty) {
		*d = *s;
		return;
	}

	struct nat64state_value old = *d;
	*d = *s;
	// The connection age is preserved across refreshes.
	d->created_at = old.created_at;
	// The session identity never changes after creation.
	if (old.ip4 != 0 || old.port != 0) {
		d->ip4 = old.ip4;
		d->port = old.port;
	}
	if (old.src6[0] | old.src6[1] | old.src6[15]) {
		memcpy(d->src6, old.src6, sizeof(d->src6));
	}
	// TCP flags accumulate across refreshes (once seen, stays seen).
	d->tcp_flags |= old.tcp_flags;
}

// Cross-layer promotion of a nat64state entry from a stale layer into a
// fresh slot of the active layer, with the same identity-wins merge.
static inline void
fwmap_promote_value_nat64state(
	void *dst, const void *new_value, const void *old_value, size_t size
) {
	assert(size == sizeof(struct nat64state_value));

	struct nat64state_value *d = (struct nat64state_value *)dst;
	const struct nat64state_value *new_v =
		(const struct nat64state_value *)new_value;
	const struct nat64state_value *old_v =
		(const struct nat64state_value *)old_value;

	*d = *new_v;
	d->created_at = old_v->created_at;
	if (old_v->ip4 != 0 || old_v->port != 0) {
		d->ip4 = old_v->ip4;
		d->port = old_v->port;
	}
	if (old_v->src6[0] | old_v->src6[1] | old_v->src6[15]) {
		memcpy(d->src6, old_v->src6, sizeof(d->src6));
	}
	d->tcp_flags = new_v->tcp_flags | old_v->tcp_flags;
}
