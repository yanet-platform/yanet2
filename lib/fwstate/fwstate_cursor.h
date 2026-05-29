#pragma once

#include <stdatomic.h>
#include <stdbool.h>
#include <stdint.h>

#include "fwmap.h"
#include "types.h"

typedef struct fwstate_cursor {
	int64_t key_pos;
	bool include_expired;
} fwstate_cursor_t;

typedef struct fwstate_cursor_entry {
	void *key;
	void *value;
	uint32_t idx;
	bool expired;
} fwstate_cursor_entry_t;

static inline int
fwstate_cursor_read_entry(
	fwmap_t *map,
	fwstate_cursor_t *cursor,
	uint64_t now,
	fwstate_cursor_entry_t *entry
) {
	struct fw_state_value *value = (struct fw_state_value *)fwmap_get_value(
		map, (uint32_t)cursor->key_pos
	);
	if (value == NULL) {
		return 0;
	}

	// Skip uninitialized entries
	if (value->updated_at == 0) {
		return 0;
	}

	void *key = fwmap_get_key(map, (uint32_t)cursor->key_pos);
	if (key == NULL) {
		return 0;
	}

	bool expired = fwstate_value_is_expired(value, now);

	if (!cursor->include_expired && expired) {
		return 0;
	}

	entry->key = key;
	entry->value = value;
	entry->idx = (uint32_t)cursor->key_pos;
	entry->expired = expired;
	return 1;
}

/// Read up to `count` entries in the forward direction (ascending key index).
/// Returns the number of entries written to `out`.
/// Returns 0 when there are no more entries.
/// Bounds-safe: out-of-range key_pos results in 0 entries returned.
static inline uint32_t
fwstate_cursor_read_forward(
	fwmap_t *map,
	fwstate_cursor_t *cursor,
	uint64_t now,
	fwstate_cursor_entry_t *out,
	uint32_t count
) {
	uint32_t key_limit =
		__atomic_load_n(&map->key_cursor, __ATOMIC_RELAXED);
	if (key_limit == 0) {
		return 0;
	}

	uint32_t collected = 0;
	while (collected < count && cursor->key_pos < (int64_t)key_limit) {
		fwstate_cursor_entry_t *entry = &out[collected];
		collected += fwstate_cursor_read_entry(map, cursor, now, entry);
		cursor->key_pos++;
	}

	return collected;
}

/// Read up to `count` entries in the backward direction (descending key
/// index). Returns the number of entries written to `out`.
/// Returns 0 when there are no more entries.
/// Bounds-safe: out-of-range key_pos is clamped to key_cursor - 1.
static inline uint32_t
fwstate_cursor_read_backward(
	fwmap_t *map,
	fwstate_cursor_t *cursor,
	uint64_t now,
	fwstate_cursor_entry_t *out,
	uint32_t count
) {
	uint32_t key_limit =
		__atomic_load_n(&map->key_cursor, __ATOMIC_RELAXED);
	if (key_limit == 0) {
		return 0;
	}

	// Clamp key_pos to valid range
	if (cursor->key_pos >= (int64_t)key_limit) {
		cursor->key_pos = (int64_t)(key_limit)-1;
	}

	uint32_t collected = 0;
	while (collected < count && cursor->key_pos >= 0) {
		fwstate_cursor_entry_t *entry = &out[collected];
		collected += fwstate_cursor_read_entry(map, cursor, now, entry);
		cursor->key_pos--;
	}

	return collected;
}
