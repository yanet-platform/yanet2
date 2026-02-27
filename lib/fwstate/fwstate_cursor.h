#pragma once

#include <netinet/in.h>
#include <stdatomic.h>
#include <stdbool.h>
#include <stdint.h>

#include "config.h"
#include "fwmap.h"

// ============================================================================
// Cursor types
// ============================================================================

typedef struct fwstate_cursor {
	uint32_t key_pos;
	bool include_expired;
	struct fwstate_timeouts timeouts;
} fwstate_cursor_t;

typedef struct fwstate_cursor_entry {
	void *key;
	void *value;
	uint32_t idx;
} fwstate_cursor_entry_t;

// ============================================================================
// TTL helper
// ============================================================================

/// Select the appropriate TTL for a firewall state entry based on its
/// protocol type and TCP flags.
static inline uint64_t
fwstate_entry_ttl(
	uint16_t proto, uint8_t raw_flags, const struct fwstate_timeouts *t
) {
	union fw_state_flags_u flags = {.raw = raw_flags};
	if (proto == IPPROTO_UDP) {
		return t->udp;
	}
	if (proto == IPPROTO_TCP) {
		if (flags.tcp.src & FWSTATE_FIN ||
		    flags.tcp.dst & FWSTATE_FIN) {
			return t->tcp_fin;
		}
		if ((flags.tcp.src & FWSTATE_SYN) &&
		    (flags.tcp.dst & FWSTATE_ACK)) {
			return t->tcp_syn_ack;
		}
		if (flags.tcp.src & FWSTATE_SYN) {
			return t->tcp_syn;
		}
		return t->tcp;
	}
	return t->default_;
}

// ============================================================================
// Cursor read functions
// ============================================================================

/// Read up to `count` entries in the forward direction (ascending key index).
/// Returns the number of entries written to `out`.
/// Returns 0 when there are no more entries.
/// Bounds-safe: out-of-range key_pos results in 0 entries returned.
static inline int32_t
fwstate_cursor_read_forward(
	fwmap_t *map,
	fwstate_cursor_t *cursor,
	uint64_t now,
	fwstate_cursor_entry_t *out,
	uint32_t count
) {
	uint32_t key_limit =
		__atomic_load_n(&map->key_cursor, __ATOMIC_RELAXED);
	int32_t collected = 0;

	while ((uint32_t)collected < count && cursor->key_pos < key_limit) {
		struct fw_state_value *value = (struct fw_state_value *)
			fwmap_get_value(map, cursor->key_pos);
		if (value == NULL) {
			cursor->key_pos++;
			continue;
		}

		// Skip uninitialized entries
		if (value->updated_at == 0) {
			cursor->key_pos++;
			continue;
		}

		void *key = fwmap_get_key(map, cursor->key_pos);
		if (key == NULL) {
			cursor->key_pos++;
			continue;
		}

		const struct fw_state_key_hdr *hdr =
			(const struct fw_state_key_hdr *)key;
		uint64_t ttl = fwstate_entry_ttl(
			hdr->proto, value->flags.raw, &cursor->timeouts
		);
		bool expired = (value->updated_at + ttl <= now);

		if (!cursor->include_expired && expired) {
			cursor->key_pos++;
			continue;
		}

		out[collected].key = key;
		out[collected].value = value;
		out[collected].idx = cursor->key_pos;
		collected++;
		cursor->key_pos++;
	}

	return collected;
}

/// Read up to `count` entries in the backward direction (descending key
/// index). Returns the number of entries written to `out`.
/// Returns 0 when there are no more entries.
/// Bounds-safe: out-of-range key_pos is clamped to key_cursor - 1.
static inline int32_t
fwstate_cursor_read_backward(
	fwmap_t *map,
	fwstate_cursor_t *cursor,
	uint64_t now,
	fwstate_cursor_entry_t *out,
	uint32_t count
) {
	uint32_t key_limit =
		__atomic_load_n(&map->key_cursor, __ATOMIC_RELAXED);
	int32_t collected = 0;

	if (key_limit == 0) {
		return 0;
	}

	// Clamp key_pos to valid range
	if (cursor->key_pos >= key_limit) {
		cursor->key_pos = key_limit - 1;
	}

	// Use signed position to detect underflow past 0
	int64_t pos = (int64_t)cursor->key_pos;

	while ((uint32_t)collected < count && pos >= 0) {
		struct fw_state_value *value = (struct fw_state_value *)
			fwmap_get_value(map, (uint32_t)pos);
		if (value == NULL) {
			pos--;
			continue;
		}

		// Skip uninitialized entries
		if (value->updated_at == 0) {
			pos--;
			continue;
		}

		void *key = fwmap_get_key(map, (uint32_t)pos);
		if (key == NULL) {
			pos--;
			continue;
		}

		const struct fw_state_key_hdr *hdr =
			(const struct fw_state_key_hdr *)key;
		uint64_t ttl = fwstate_entry_ttl(
			hdr->proto, value->flags.raw, &cursor->timeouts
		);
		bool expired = (value->updated_at + ttl <= now);

		if (!cursor->include_expired && expired) {
			pos--;
			continue;
		}

		out[collected].key = key;
		out[collected].value = value;
		out[collected].idx = (uint32_t)pos;
		collected++;
		pos--;
	}

	// Update cursor position for next call
	if (pos < 0) {
		cursor->key_pos = 0;
	} else {
		cursor->key_pos = (uint32_t)pos;
	}

	return collected;
}
