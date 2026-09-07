#pragma once

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>
#include <string.h>

#include "lib/statemap/fwmap.h"
#include "lib/statemap/fwtable.h"

// Default lifetime of an l3 state record, in seconds; an entry past its
// deadline is treated as absent.
#define L3S_TTL_SECONDS 300

// Default hash index size of a table layer, in slots.
#define L3S_INDEX_SIZE (1024u * 1024u)

// Default extra bucket count of a table layer.
#define L3S_EXTRA_BUCKETS 1024u

/*
 * Flow identity of an l3 state record: the client's source address and
 * source port.
 *
 * family discriminates the address family and selects how many bytes of
 * src_addr carry the address (4 for IPv4, zero-padded; 16 for IPv6), so one
 * table holds both families without cross-family key collisions. The
 * reserved byte keeps src_port aligned and must stay zero.
 */
struct l3s_key {
	uint8_t family;
	uint8_t reserved;
	uint16_t src_port; // little-endian
	uint8_t src_addr[16];
};

/*
 * The identity of the real server a flow is pinned to.
 *
 * Sessions outlive service updates, and a positional index would silently
 * denote a different backend after the real server list changes; the
 * destination address resolves against whatever list the current service
 * object carries, so a pinned flow keeps its backend or, when the backend is
 * gone or disabled, re-schedules and re-pins.
 */
struct l3s_value {
	// Address family of the pinned real server: 4 or 6.
	uint8_t family;
	uint8_t reserved;
	// Destination address of the pinned real server; IPv4 uses the first
	// 4 bytes, zero-padded.
	uint8_t destination[16];
};

// Record the identity of a real server. addr carries 4 bytes for family 4
// and 16 bytes for family 6.
static inline void
l3s_value_of(uint8_t family, const uint8_t *addr, struct l3s_value *value) {
	memset(value, 0, sizeof(*value));
	value->family = family;
	size_t width = family == 4 ? 4 : 16;
	memcpy(value->destination, addr, width);
}

/*
 * Fill a layer configuration for an l3 state table.
 *
 * worker_count must cover every worker that will insert records; index_size
 * and extra_bucket_count size the layer's hash index and bucket store (zero
 * selects the defaults). Records re-pin in place on update and keep their
 * value when promoted from a deeper layer.
 */
static inline void
l3s_config(
	uint16_t worker_count,
	uint32_t index_size,
	uint32_t extra_bucket_count,
	fwmap_config_t *config
) {
	config->key_size = sizeof(struct l3s_key);
	config->value_size = sizeof(struct l3s_value);
	config->hash_fn_id = FWMAP_HASH_FNV1A;
	config->rand_fn_id = FWMAP_RAND_DEFAULT;
	config->copy_key_fn_id = FWMAP_COPY_KEY_DEFAULT;
	config->update_value_fn_id = FWMAP_UPDATE_VALUE_DEFAULT;
	config->promote_value_fn_id = FWMAP_PROMOTE_VALUE_KEEP_OLD;
	config->worker_count = worker_count;
	config->index_size = index_size != 0 ? index_size : L3S_INDEX_SIZE;
	config->extra_bucket_count = extra_bucket_count != 0
					     ? extra_bucket_count
					     : L3S_EXTRA_BUCKETS;
}

// Default record lifetime in nanoseconds, for callers passing a ttl.
static inline uint64_t
l3s_default_ttl(void) {
	return L3S_TTL_SECONDS * 1000000000ull;
}

/*
 * Look up the real server a flow is pinned to.
 *
 * Returns 0 and copies the pinned real server's identity on hit, or -1 when
 * the table has no live record for the key. Entries past their deadline count
 * as absent.
 */
static inline int
l3s_table_lookup(
	fwtable_t *table,
	uint64_t now,
	const struct l3s_key *key,
	struct l3s_value *pinned
) {
	struct l3s_value *value = NULL;
	rwlock_t *lock = NULL;
	bool from_stale = false;

	int64_t result = fwtable_lookup(
		table, now, key, (void **)&value, &lock, &from_stale
	);
	if (result < 0 || value == NULL) {
		if (lock != NULL) {
			rwlock_read_unlock(lock);
		}
		return -1;
	}

	struct l3s_value candidate = *value;
	if (lock != NULL) {
		rwlock_read_unlock(lock);
	}

	*pinned = candidate;
	return 0;
}

/*
 * Pin a flow to a real server.
 *
 * Inserts the record into the active layer with the given time-to-live; an
 * existing record in a deeper layer is promoted first-write-wins, so a flow
 * keeps the backend it was pinned to across layer rotations. Returns 0 on
 * success, -1 when the active layer is full.
 */
static inline int
l3s_table_insert(
	fwtable_t *table,
	uint16_t worker_idx,
	uint64_t now,
	uint64_t ttl,
	const struct l3s_key *key,
	const struct l3s_value *value
) {
	rwlock_t *lock = NULL;

	int64_t result =
		fwtable_insert(table, worker_idx, now, ttl, key, value, &lock);

	if (lock != NULL) {
		rwlock_write_unlock(lock);
	}
	return result < 0 ? -1 : 0;
}
