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

// Default hash index size of a table layer, in slots. Sized so a default
// service with its table fits a default agent arena alongside the module's
// other allocations; the control plane can raise it per service.
#define L3S_INDEX_SIZE (64u * 1024u)

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
	// Address family of the pinned real server: 4 or 6. Any other value
	// marks an unoccupied slot.
	uint8_t family;
	uint8_t reserved;
	// Absolute record deadline in nanoseconds; stamped by the insert from
	// its time-to-live and carried along on promotion.
	uint64_t expires_at;
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
	// Stamp the record's absolute deadline so readers can report it
	// without reaching into the map's bucket metadata; a promoted record
	// keeps the deadline of the value it inherits.
	struct l3s_value stamped = *value;
	stamped.expires_at = now + ttl;

	rwlock_t *lock = NULL;

	int64_t result = fwtable_insert(
		table, worker_idx, now, ttl, key, &stamped, &lock
	);

	if (lock != NULL) {
		rwlock_write_unlock(lock);
	}
	return result < 0 ? -1 : 0;
}

/*
 * Cursor over the records of a layered table.
 *
 * The token packs (layer << 32) | slot, ascending; L3S_CURSOR_DONE marks an
 * exhausted listing.
 */
typedef struct l3s_cursor {
	uint64_t token;
} l3s_cursor_t;

#define L3S_CURSOR_DONE 0xffffffffffffffff

/*
 * One decoded session record.
 */
struct l3s_session {
	struct l3s_key key;
	struct l3s_value value;
};

/*
 * Read up to capacity session records starting at the cursor, skipping empty
 * and expired slots, and advance the cursor past the last record read.
 *
 * Reads are unsynchronized slot reads taken while the dataplane may be
 * writing: a record can be moments out of date, which observability traffic
 * tolerates. Returns the number of records read; the cursor is left at
 * L3S_CURSOR_DONE when the table is exhausted.
 */
static inline uint32_t
l3s_table_read(
	const fwtable_t *table,
	uint64_t now,
	l3s_cursor_t *cursor,
	struct l3s_session *sessions,
	uint32_t capacity
) {
	uint32_t collected = 0;
	uint32_t layer_idx = (uint32_t)(cursor->token >> 32);
	if (cursor->token == L3S_CURSOR_DONE) {
		return 0;
	}
	uint32_t slot = (uint32_t)(cursor->token & 0xffffffffull);

	fwmap_t *layer = ATOMIC_ADDR_OF(&((fwtable_t *)table)->head);
	for (uint32_t idx = 0; layer != NULL;
	     ++idx, layer = (fwmap_t *)ATOMIC_ADDR_OF(&layer->next)) {
		if (idx < layer_idx) {
			continue;
		}

		uint32_t key_limit =
			__atomic_load_n(&layer->key_cursor, __ATOMIC_RELAXED);
		for (; slot < key_limit && collected < capacity; ++slot) {
			const struct l3s_value *value = (struct l3s_value *)
				fwmap_get_value(layer, slot);
			if (value == NULL ||
			    (value->family != 4 && value->family != 6)) {
				continue;
			}
			if (value->expires_at <= now) {
				continue;
			}

			const struct l3s_key *key =
				(struct l3s_key *)fwmap_get_key(layer, slot);
			if (key == NULL) {
				continue;
			}

			// A writer may be re-pinning the same slot under its
			// bucket lock; copying the pair twice and keeping it
			// only when both reads agree yields either the old or
			// the new record, never a torn mix, and a mismatching
			// slot is simply skipped this pass.
			struct l3s_session first_copy = {
				.key = *key,
				.value = *value,
			};
			if (value->family != first_copy.value.family ||
			    value->expires_at != first_copy.value.expires_at ||
			    key->src_port != first_copy.key.src_port ||
			    memcmp(key->src_addr,
				   first_copy.key.src_addr,
				   sizeof(key->src_addr)) != 0 ||
			    memcmp(value->destination,
				   first_copy.value.destination,
				   sizeof(value->destination)) != 0) {
				continue;
			}

			sessions[collected] = first_copy;
			collected += 1;
		}

		if (collected == capacity) {
			cursor->token =
				((uint64_t)(idx) << 32) | (uint64_t)slot;
			return collected;
		}

		layer_idx = idx + 1;
		slot = 0;
	}

	cursor->token = L3S_CURSOR_DONE;
	return collected;
}
