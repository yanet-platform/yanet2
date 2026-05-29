#pragma once

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>

#include "types.h"

// === Custom copy functions for fwmap keys and values.

static inline void
fwmap_copy_key_fw4(void *dst, const void *src, size_t size) {
	(void)size;
	const struct fw4_state_key *s = (const struct fw4_state_key *)src;
	struct fw4_state_key *d = (struct fw4_state_key *)dst;

	*d = *s;
}

static inline void
fwmap_copy_key_fw6(void *dst, const void *src, size_t size) {
	(void)size;
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
	(void)size;
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
	(void)size;

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
	(void)size;
	const struct fw4_state_key *k1 = (const struct fw4_state_key *)a;
	const struct fw4_state_key *k2 = (const struct fw4_state_key *)b;

	return k1->hdr.proto == k2->hdr.proto &&
	       k1->hdr.src_port == k2->hdr.src_port &&
	       k1->hdr.dst_port == k2->hdr.dst_port &&
	       k1->src_addr == k2->src_addr && k1->dst_addr == k2->dst_addr;
}

static inline bool
fwmap_fw6_key_equal(const void *a, const void *b, size_t size) {
	(void)size;
	const struct fw6_state_key *k1 = (const struct fw6_state_key *)a;
	const struct fw6_state_key *k2 = (const struct fw6_state_key *)b;

	// Compare IPv6 addresses as two uint64_t values each
	const uint64_t *src1 = (const uint64_t *)k1->src_addr;
	const uint64_t *src2 = (const uint64_t *)k2->src_addr;
	const uint64_t *dst1 = (const uint64_t *)k1->dst_addr;
	const uint64_t *dst2 = (const uint64_t *)k2->dst_addr;

	return k1->hdr.proto == k2->hdr.proto &&
	       k1->hdr.src_port == k2->hdr.src_port &&
	       k1->hdr.dst_port == k2->hdr.dst_port && src1[0] == src2[0] &&
	       src1[1] == src2[1] && dst1[0] == dst2[0] && dst1[1] == dst2[1];
}
