#pragma once

#include <errno.h>
#include <stdint.h>
#include <string.h>

#include "lib/fwstate/types.h"
#include "lib/statemap/fwmap.h"

enum fwmap_probe_case {
	FRESH,
	MATCH_LIVE,
	MATCH_EQUAL_DEADLINE,
	MATCH_EXPIRED,
	REUSE_EQUAL_DEADLINE,
	REUSE_EXPIRED,
	ZERO_TTL,
	OVERFLOW_EXHAUSTED,
	KEY_EXHAUSTED_AFTER_LINK,
	MATCH_AFTER_EXPIRED,
	ZERO_DEADLINE_TERMINATES,
};

enum {
	FWMAP_PROBE_KEYS = 1024,
	FWMAP_PROBE_ROOTS = 256,
	FWMAP_PROBE_EXTRA = 64,
	FWMAP_PROBE_WORKERS = 2
};

struct fwmap_probe_bucket {
	uint32_t next;
	uint16_t sig[FWMAP_BUCKET_ENTRIES];
	uint64_t deadline[FWMAP_BUCKET_ENTRIES];
	uint32_t idx[FWMAP_BUCKET_ENTRIES];
};

struct fwmap_probe_snapshot {
	uint32_t key_cursor, extra_free_idx, root_next;
	uint64_t total_elements[FWMAP_PROBE_WORKERS];
	uint64_t max_chain[FWMAP_PROBE_WORKERS];
	uint64_t max_deadline[FWMAP_PROBE_WORKERS];
	struct fwmap_probe_bucket roots[FWMAP_PROBE_ROOTS];
	struct fwmap_probe_bucket extra[FWMAP_PROBE_EXTRA];
	struct fw4_state_key keys[FWMAP_PROBE_KEYS];
	struct fw_state_value values[FWMAP_PROBE_KEYS];
};

struct fwmap_probe_result {
	struct fwmap_probe_snapshot before, discovered, committed;
	struct fw4_state_key query;
	uint32_t root_idx, result_idx;
	int64_t lookup_idx;
	uint64_t lookup_packets;
	uint32_t matching_slots;
	int result_has_key, result_has_value, result_empty;
	int lock_is_root, lock_before, lock_discovered, lock_committed,
		lock_released;
};

// Capture all mutable map storage, excluding locks.
static inline void
fwmap_probe_snapshot(
	fwmap_t *map, uint32_t root_idx, struct fwmap_probe_snapshot *out
) {
	fwmap_bucket_t **chunks = ADDR_OF(&map->buckets);
	fwmap_bucket_t *roots = ADDR_OF(&chunks[0]);
	fwmap_bucket_t *extra = ADDR_OF(&map->extra_buckets);
	out->key_cursor = map->key_cursor;
	out->extra_free_idx = map->extra_free_idx;
	out->root_next = roots[root_idx].next;
	for (uint32_t i = 0; i < FWMAP_PROBE_WORKERS; ++i) {
		out->total_elements[i] = map->counters[i].total_elements;
		out->max_chain[i] = map->counters[i].max_chain;
		out->max_deadline[i] = map->counters[i].max_deadline;
	}
	for (uint32_t i = 0; i < FWMAP_PROBE_ROOTS + FWMAP_PROBE_EXTRA; ++i) {
		fwmap_bucket_t *src = i < FWMAP_PROBE_ROOTS
					      ? &roots[i]
					      : &extra[i - FWMAP_PROBE_ROOTS];
		struct fwmap_probe_bucket *dst =
			i < FWMAP_PROBE_ROOTS
				? &out->roots[i]
				: &out->extra[i - FWMAP_PROBE_ROOTS];
		dst->next = src->next;
		memcpy(dst->sig, src->sig, sizeof(dst->sig));
		memcpy(dst->deadline, src->deadline, sizeof(dst->deadline));
		memcpy(dst->idx, src->idx, sizeof(dst->idx));
	}
	for (uint32_t i = 0; i < FWMAP_PROBE_KEYS; ++i) {
		memcpy(&out->keys[i],
		       fwmap_get_key(map, i),
		       sizeof(out->keys[i]));
		memcpy(&out->values[i],
		       fwmap_get_value(map, i),
		       sizeof(out->values[i]));
	}
}

// Exercise discovery and commit on an otherwise unused real IPv4 map.
static inline int
fwmap_probe_entry(
	void *raw_map,
	enum fwmap_probe_case scenario,
	int use_lock,
	struct fwmap_probe_result *out
) {
	fwmap_t *map = raw_map;
	if (!map || !out || scenario < FRESH ||
	    scenario > ZERO_DEADLINE_TERMINATES ||
	    map->index_size != FWMAP_PROBE_KEYS ||
	    map->extra_size != FWMAP_PROBE_EXTRA ||
	    map->worker_count != FWMAP_PROBE_WORKERS ||
	    map->key_size != sizeof(struct fw4_state_key) ||
	    map->value_size != sizeof(struct fw_state_value) ||
	    map->key_cursor != 0 || map->extra_free_idx != 1 ||
	    map->hash_fn_id != FWMAP_HASH_FNV1A) {
		return -EINVAL;
	}
	memset(out, 0, sizeof(*out));
	for (uint32_t i = 0; i < FWMAP_PROBE_KEYS; ++i) {
		memset(fwmap_get_key(map, i), 0, map->key_size);
		memset(fwmap_get_value(map, i), 0, map->value_size);
	}
	map->hash_seed = 1;
	fwmap_hash_fn_t hash =
		(fwmap_hash_fn_t)fwmap_func_registry[map->hash_fn_id];
	struct fw4_state_key key = {
		.hdr = {.proto = 17, .src_port = 1234, .dst_port = 53}
	};
	struct fw_state_value value = {
		.created_at = 11, .updated_at = 12, .packets_forward = 13
	};
	uint32_t root_idx = (uint32_t)hash(&key, sizeof(key), map->hash_seed) &
			    map->index_mask;
	unsigned count = 0;
	if (scenario == MATCH_LIVE || scenario == MATCH_EQUAL_DEADLINE ||
	    scenario == MATCH_EXPIRED) {
		count = 1;
	} else if (
		scenario == REUSE_EQUAL_DEADLINE || scenario == REUSE_EXPIRED ||
		scenario == KEY_EXHAUSTED_AFTER_LINK
	) {
		count = 4;
	} else if (scenario == OVERFLOW_EXHAUSTED) {
		count = FWMAP_PROBE_EXTRA * FWMAP_BUCKET_ENTRIES;
	} else if (scenario == MATCH_AFTER_EXPIRED) {
		count = 5;
	} else if (scenario == ZERO_DEADLINE_TERMINATES) {
		count = 2;
	}
	uint32_t candidate = 0;
	struct fw4_state_key first = key;
	struct fw4_state_key later = key;
	for (unsigned i = 0; i < count; ++i) {
		while (((uint32_t)hash(&key, sizeof(key), map->hash_seed) &
			map->index_mask) != root_idx) {
			if (++candidate == 0) {
				return -ERANGE;
			}
			key.src_addr = candidate;
		}
		uint64_t deadline = 200;
		if (i == 0 && (scenario == MATCH_EQUAL_DEADLINE ||
			       scenario == REUSE_EQUAL_DEADLINE)) {
			deadline = 100;
		} else if (
			i == 0 && (scenario == MATCH_EXPIRED ||
				   scenario == REUSE_EXPIRED ||
				   scenario == MATCH_AFTER_EXPIRED)
		) {
			deadline = 99;
		}
		if (fwmap_put(map, 0, 1, deadline - 1, &key, &value, NULL) !=
		    (int64_t)i) {
			return -EIO;
		}
		if ((scenario == MATCH_AFTER_EXPIRED && i == 4) ||
		    (scenario == ZERO_DEADLINE_TERMINATES && i == 1)) {
			later = key;
		}
		if (++candidate == 0) {
			return -ERANGE;
		}
		key.src_addr = candidate;
	}
	while (((uint32_t)hash(&key, sizeof(key), map->hash_seed) &
		map->index_mask) != root_idx) {
		if (++candidate == 0) {
			return -ERANGE;
		}
		key.src_addr = candidate;
	}
	if (scenario == MATCH_LIVE || scenario == MATCH_EQUAL_DEADLINE ||
	    scenario == MATCH_EXPIRED) {
		key = first;
	} else if (
		scenario == MATCH_AFTER_EXPIRED ||
		scenario == ZERO_DEADLINE_TERMINATES
	) {
		key = later;
	}
	if (scenario == KEY_EXHAUSTED_AFTER_LINK) {
		while (fwmap_next_free_key(map) >= 0) {
		}
	}
	fwmap_bucket_t **chunks = ADDR_OF(&map->buckets);
	fwmap_bucket_t *roots = ADDR_OF(&chunks[0]);
	if (scenario == ZERO_DEADLINE_TERMINATES) {
		roots[root_idx].deadline[0] = 0;
	}
	rwlock_t *root_lock = &roots[root_idx].lock;
	rwlock_t *lock = NULL;
	out->query = key;
	out->root_idx = root_idx;
	out->lock_before = atomic_load(&root_lock->cnt);
	fwmap_probe_snapshot(map, root_idx, &out->before);
	struct fwmap_entry_discovery discovery =
		fwmap_entry_discover(map, 100, &key, use_lock ? &lock : NULL);
	out->lock_is_root = lock == root_lock;
	out->lock_discovered = atomic_load(&root_lock->cnt);
	fwmap_probe_snapshot(map, root_idx, &out->discovered);
	fwmap_entry_t entry = fwmap_entry_commit(
		map, 0, 100, scenario == ZERO_TTL ? 0 : 50, discovery
	);
	out->result_idx = entry.idx;
	out->result_has_key = entry.key != NULL;
	out->result_has_value = entry.value != NULL;
	out->result_empty = entry.empty;
	out->lock_committed = atomic_load(&root_lock->cnt);
	fwmap_probe_snapshot(map, root_idx, &out->committed);
	if (scenario == MATCH_AFTER_EXPIRED) {
		fwmap_bucket_t *extra = ADDR_OF(&map->extra_buckets);
		fwmap_bucket_t *bucket = &roots[root_idx];
		while (bucket) {
			for (uint32_t i = 0; i < FWMAP_BUCKET_ENTRIES; ++i) {
				if (bucket->deadline[i] &&
				    memcmp(fwmap_get_key(map, bucket->idx[i]),
					   &key,
					   sizeof(key)) == 0) {
					++out->matching_slots;
				}
			}
			bucket = bucket->next ? &extra[bucket->next] : NULL;
		}
	}
	if (entry.key && entry.empty) {
		memcpy(entry.key, &key, sizeof(key));
		memcpy(entry.value, &value, sizeof(value));
	}
	if (lock) {
		rwlock_write_unlock(lock);
	}
	out->lock_released = atomic_load(&root_lock->cnt);
	void *lookup_value = NULL;
	out->lookup_idx = fwmap_get(map, 100, &key, &lookup_value, NULL);
	if (lookup_value) {
		out->lookup_packets = ((struct fw_state_value *)lookup_value)
					      ->packets_forward;
	}
	return 0;
}
