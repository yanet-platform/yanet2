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

static inline void
fwmap_copy_value_fwstate(void *dst, const void *src, size_t size) {
	(void)size;
	const struct fw_state_value *s = (const struct fw_state_value *)src;
	struct fw_state_value *d = (struct fw_state_value *)dst;

	*d = *s;
}

static inline void
fwmap_merge_value_fwstate(
	void *dst, const void *new_value, const void *old_value, size_t size
) {
	(void)size;

	struct fw_state_value *d = (struct fw_state_value *)dst;
	const struct fw_state_value *new_v =
		(const struct fw_state_value *)new_value;
	const struct fw_state_value *old_v =
		(const struct fw_state_value *)old_value;

	// Update
	d->external = new_v->external;
	d->type = new_v->type;
	d->packets_since_last_sync = new_v->packets_since_last_sync;
	d->last_sync = new_v->last_sync;

	// Merge
	d->flags.raw = new_v->flags.raw | old_v->flags.raw;
	d->packets_backward = new_v->packets_backward + old_v->packets_backward;
	d->packets_forward = new_v->packets_forward + old_v->packets_forward;
	return;
}

// == Custom key comparison functions for fwstate keys.

static inline bool
fwmap_fw4_key_equal(const void *a, const void *b, size_t size) {
	(void)size;
	const struct fw4_state_key *k1 = (const struct fw4_state_key *)a;
	const struct fw4_state_key *k2 = (const struct fw4_state_key *)b;

	return k1->proto == k2->proto && k1->src_port == k2->src_port &&
	       k1->dst_port == k2->dst_port && k1->src_addr == k2->src_addr &&
	       k1->dst_addr == k2->dst_addr;
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

	return k1->proto == k2->proto && k1->src_port == k2->src_port &&
	       k1->dst_port == k2->dst_port && src1[0] == src2[0] &&
	       src1[1] == src2[1] && dst1[0] == dst2[0] && dst1[1] == dst2[1];
}
