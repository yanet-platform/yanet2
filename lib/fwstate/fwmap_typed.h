#pragma once

#include <stddef.h>
#include <stdint.h>

#include "fwmap.h"
#include "fwstate_cursor.h"
#include "layermap.h"
#include "types.h"

/*
 * Typed fwmap handles — bind the generic fwmap_t to a specific key family so
 * that a key/map family mismatch (e.g. feeding a struct fw6_state_key to an
 * fw4state map) becomes a compile-time error instead of silent truncation UB.
 *
 * Each handle is a thin stack-local wrapper around the resolved fwmap_t
 * pointer. It carries no extra state and is NOT stored in shared memory:
 * the shared-memory layout (fwmap_t, fwstate_config) is unchanged. Callers
 * resolve the offset to a raw fwmap_t * and wrap it at the point of use.
 *
 * Sizes are baked into the typed constructors, so production callers can no
 * longer influence key_size/value_size — the field becomes an internal
 * invariant of fwmap4_new/fwmap6_new rather than a contract every caller
 * has to remember.
 */

typedef struct fwmap4 {
	fwmap_t *raw;
} fwmap4_t;
typedef struct fwmap6 {
	fwmap_t *raw;
} fwmap6_t;

static inline fwmap4_t
fwmap4_from_raw(fwmap_t *raw) {
	return (fwmap4_t){.raw = raw};
}

static inline fwmap6_t
fwmap6_from_raw(fwmap_t *raw) {
	return (fwmap6_t){.raw = raw};
}

static inline fwmap_t *
fwmap4_raw(fwmap4_t m) {
	return m.raw;
}

static inline fwmap_t *
fwmap6_raw(fwmap6_t m) {
	return m.raw;
}

/* ====================================================================
 * Construction / destruction
 * ==================================================================== */

#define FWMAP4_DEFAULT_INDEX_SIZE (1024u * 1024u)
#define FWMAP6_DEFAULT_INDEX_SIZE (1024u * 1024u)
#define FWMAP_DEFAULT_EXTRA_BUCKETS 1024u

static inline fwmap4_t
fwmap4_new(
	uint32_t index_size,
	uint32_t extra_bucket_count,
	uint16_t worker_count,
	struct memory_context *ctx
) {
	if (index_size == 0) {
		index_size = FWMAP4_DEFAULT_INDEX_SIZE;
	}
	if (extra_bucket_count == 0) {
		extra_bucket_count = FWMAP_DEFAULT_EXTRA_BUCKETS;
	}

	fwmap_config_t cfg = {
		.key_size = sizeof(struct fw4_state_key),
		.value_size = sizeof(struct fw_state_value),
		.hash_seed = 0,
		.worker_count = worker_count,
		.index_size = index_size,
		.extra_bucket_count = extra_bucket_count,
		.hash_fn_id = FWMAP_HASH_FNV1A,
		.key_equal_fn_id = FWMAP_KEY_EQUAL_FW4,
		.rand_fn_id = FWMAP_RAND_DEFAULT,
		.copy_key_fn_id = FWMAP_COPY_KEY_FW4,
		.update_value_fn_id = FWMAP_UPDATE_VALUE_FWSTATE,
		.promote_value_fn_id = FWMAP_PROMOTE_VALUE_FWSTATE,
	};

	return fwmap4_from_raw(fwmap_new(&cfg, ctx));
}

static inline fwmap6_t
fwmap6_new(
	uint32_t index_size,
	uint32_t extra_bucket_count,
	uint16_t worker_count,
	struct memory_context *ctx
) {
	if (index_size == 0) {
		index_size = FWMAP6_DEFAULT_INDEX_SIZE;
	}
	if (extra_bucket_count == 0) {
		extra_bucket_count = FWMAP_DEFAULT_EXTRA_BUCKETS;
	}

	fwmap_config_t cfg = {
		.key_size = sizeof(struct fw6_state_key),
		.value_size = sizeof(struct fw_state_value),
		.hash_seed = 0,
		.worker_count = worker_count,
		.index_size = index_size,
		.extra_bucket_count = extra_bucket_count,
		.hash_fn_id = FWMAP_HASH_FNV1A,
		.key_equal_fn_id = FWMAP_KEY_EQUAL_FW6,
		.rand_fn_id = FWMAP_RAND_DEFAULT,
		.copy_key_fn_id = FWMAP_COPY_KEY_FW6,
		.update_value_fn_id = FWMAP_UPDATE_VALUE_FWSTATE,
		.promote_value_fn_id = FWMAP_PROMOTE_VALUE_FWSTATE,
	};

	return fwmap6_from_raw(fwmap_new(&cfg, ctx));
}

static inline void
fwmap4_free(fwmap4_t m, struct memory_context *ctx) {
	fwmap_free(m.raw, ctx);
}

static inline void
fwmap6_free(fwmap6_t m, struct memory_context *ctx) {
	fwmap_free(m.raw, ctx);
}

/* ====================================================================
 * Typed lookup / insert (hot path)
 *
 * These are the correctness-critical operations: the key and value
 * parameters are typed, so a caller cannot pair a struct fw6_state_key
 * with an fw4 map (or vice versa) — the mismatch is caught at compile
 * time. Each wrapper asserts that the underlying map's stored sizes
 * match the baked-in compile-time sizes, guarding against a wrong raw
 * pointer being wrapped.
 * ==================================================================== */

static inline int64_t
fwmap4_get_value_and_deadline(
	fwmap4_t m,
	uint64_t now,
	const struct fw4_state_key *key,
	struct fw_state_value **value,
	rwlock_t **lock,
	uint64_t *deadline,
	bool *value_from_stale_layer
) {
	assert(m.raw->key_size == sizeof(struct fw4_state_key));
	assert(m.raw->value_size == sizeof(struct fw_state_value));
	return layermap_get_value_and_deadline(
		m.raw,
		now,
		key,
		(void **)value,
		lock,
		deadline,
		value_from_stale_layer
	);
}

static inline int64_t
fwmap6_get_value_and_deadline(
	fwmap6_t m,
	uint64_t now,
	const struct fw6_state_key *key,
	struct fw_state_value **value,
	rwlock_t **lock,
	uint64_t *deadline,
	bool *value_from_stale_layer
) {
	assert(m.raw->key_size == sizeof(struct fw6_state_key));
	assert(m.raw->value_size == sizeof(struct fw_state_value));
	return layermap_get_value_and_deadline(
		m.raw,
		now,
		key,
		(void **)value,
		lock,
		deadline,
		value_from_stale_layer
	);
}

static inline int64_t
fwmap4_put(
	fwmap4_t m,
	uint16_t worker_idx,
	uint64_t now,
	uint64_t ttl,
	const struct fw4_state_key *key,
	const struct fw_state_value *value,
	rwlock_t **lock
) {
	assert(m.raw->key_size == sizeof(struct fw4_state_key));
	assert(m.raw->value_size == sizeof(struct fw_state_value));
	return layermap_put(m.raw, worker_idx, now, ttl, key, value, lock);
}

static inline int64_t
fwmap6_put(
	fwmap6_t m,
	uint16_t worker_idx,
	uint64_t now,
	uint64_t ttl,
	const struct fw6_state_key *key,
	const struct fw_state_value *value,
	rwlock_t **lock
) {
	assert(m.raw->key_size == sizeof(struct fw6_state_key));
	assert(m.raw->value_size == sizeof(struct fw_state_value));
	return layermap_put(m.raw, worker_idx, now, ttl, key, value, lock);
}

/* ====================================================================
 * Layer management (control plane)
 *
 * layermap_insert_new_layer_cp and layermap_trim_stale_layers_cp operate
 * on the shared-memory offset field (fwmap_t **) and need a config only
 * for layer creation. The typed insert wrappers bake in the config; trim
 * is family-agnostic (it only inspects deadlines) and is exposed as a
 * thin pass-through for symmetry.
 * ==================================================================== */

static inline int
fwmap4_insert_new_layer_cp(
	fwmap_t **active_layer_offset,
	uint32_t index_size,
	uint32_t extra_bucket_count,
	uint16_t worker_count,
	struct memory_context *ctx
) {
	if (index_size == 0) {
		index_size = FWMAP4_DEFAULT_INDEX_SIZE;
	}
	if (extra_bucket_count == 0) {
		extra_bucket_count = FWMAP_DEFAULT_EXTRA_BUCKETS;
	}

	fwmap_config_t cfg = {
		.key_size = sizeof(struct fw4_state_key),
		.value_size = sizeof(struct fw_state_value),
		.hash_seed = 0,
		.worker_count = worker_count,
		.index_size = index_size,
		.extra_bucket_count = extra_bucket_count,
		.hash_fn_id = FWMAP_HASH_FNV1A,
		.key_equal_fn_id = FWMAP_KEY_EQUAL_FW4,
		.rand_fn_id = FWMAP_RAND_DEFAULT,
		.copy_key_fn_id = FWMAP_COPY_KEY_FW4,
		.update_value_fn_id = FWMAP_UPDATE_VALUE_FWSTATE,
		.promote_value_fn_id = FWMAP_PROMOTE_VALUE_FWSTATE,
	};

	return layermap_insert_new_layer_cp(active_layer_offset, &cfg, ctx);
}

static inline int
fwmap6_insert_new_layer_cp(
	fwmap_t **active_layer_offset,
	uint32_t index_size,
	uint32_t extra_bucket_count,
	uint16_t worker_count,
	struct memory_context *ctx
) {
	if (index_size == 0) {
		index_size = FWMAP6_DEFAULT_INDEX_SIZE;
	}
	if (extra_bucket_count == 0) {
		extra_bucket_count = FWMAP_DEFAULT_EXTRA_BUCKETS;
	}

	fwmap_config_t cfg = {
		.key_size = sizeof(struct fw6_state_key),
		.value_size = sizeof(struct fw_state_value),
		.hash_seed = 0,
		.worker_count = worker_count,
		.index_size = index_size,
		.extra_bucket_count = extra_bucket_count,
		.hash_fn_id = FWMAP_HASH_FNV1A,
		.key_equal_fn_id = FWMAP_KEY_EQUAL_FW6,
		.rand_fn_id = FWMAP_RAND_DEFAULT,
		.copy_key_fn_id = FWMAP_COPY_KEY_FW6,
		.update_value_fn_id = FWMAP_UPDATE_VALUE_FWSTATE,
		.promote_value_fn_id = FWMAP_PROMOTE_VALUE_FWSTATE,
	};

	return layermap_insert_new_layer_cp(active_layer_offset, &cfg, ctx);
}

/* ====================================================================
 * Stats / counters (family-agnostic; typed for API symmetry)
 * ==================================================================== */

static inline fwmap_stats_t
fwmap4_get_stats(fwmap4_t m) {
	return fwmap_get_stats(m.raw);
}

static inline fwmap_stats_t
fwmap6_get_stats(fwmap6_t m) {
	return fwmap_get_stats(m.raw);
}

static inline uint32_t
fwmap4_layer_count(fwmap4_t m) {
	return fwmap_layer_count(m.raw);
}

static inline uint32_t
fwmap6_layer_count(fwmap6_t m) {
	return fwmap_layer_count(m.raw);
}

static inline uint64_t
fwmap4_max_deadline(fwmap4_t m) {
	return fwmap_max_deadline(m.raw);
}

static inline uint64_t
fwmap6_max_deadline(fwmap6_t m) {
	return fwmap_max_deadline(m.raw);
}

/* ====================================================================
 * Cursor helpers (index-based; typed for API symmetry)
 * ==================================================================== */

static inline uint32_t
fwmap4_cursor_read_forward(
	fwmap4_t m,
	fwstate_cursor_t *cursor,
	uint64_t now,
	fwstate_cursor_entry_t *out,
	uint32_t count
) {
	return fwstate_cursor_read_forward(m.raw, cursor, now, out, count);
}

static inline uint32_t
fwmap6_cursor_read_forward(
	fwmap6_t m,
	fwstate_cursor_t *cursor,
	uint64_t now,
	fwstate_cursor_entry_t *out,
	uint32_t count
) {
	return fwstate_cursor_read_forward(m.raw, cursor, now, out, count);
}

static inline uint32_t
fwmap4_cursor_read_backward(
	fwmap4_t m,
	fwstate_cursor_t *cursor,
	uint64_t now,
	fwstate_cursor_entry_t *out,
	uint32_t count
) {
	return fwstate_cursor_read_backward(m.raw, cursor, now, out, count);
}

static inline uint32_t
fwmap6_cursor_read_backward(
	fwmap6_t m,
	fwstate_cursor_t *cursor,
	uint64_t now,
	fwstate_cursor_entry_t *out,
	uint32_t count
) {
	return fwstate_cursor_read_backward(m.raw, cursor, now, out, count);
}
