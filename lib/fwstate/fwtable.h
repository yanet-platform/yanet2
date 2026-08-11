#pragma once

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>

#include "common/memory.h"
#include "common/rwlock.h"
#include "fwmap.h"

// A multi-layer firewall-state table.
//
// The head fwmap is the active (read-write) layer; each layer reachable
// through fwmap_t::next is progressively older.  The control plane
// grows the chain with fwtable_insert_layer_cp and reclaims expired
// layers with fwtable_trim_stale_cp.  The dataplane searches the chain
// with fwtable_lookup and writes through fwtable_insert.
//
// Trim reuses fwmap_t::next to thread stale layers, so the dataplane
// reads the chain and table head with acquire loads and the control
// plane publishes them with release stores; see fwtable_trim_stale_cp
// for the reader-lifetime precondition.
typedef struct fwtable {
	fwmap_t *head;
	fwmap_t *stale;
} fwtable_t;

// Prepend a newly allocated fwmap as the active head.
//
// The previous head becomes the first stale layer.  Works on an empty
// table (head == NULL) to install the very first layer.
//
// Returns 0 on success or -1 on allocation failure.
static inline int
fwtable_insert_layer_cp(
	fwtable_t *table,
	const fwmap_config_t *config,
	struct memory_context *ctx
) {
	fwmap_t *new_layer = fwmap_new(config, ctx);
	if (!new_layer) {
		return -1;
	}

	fwmap_t *head = ADDR_OF(&table->head);
	SET_OFFSET_OF(&new_layer->next, head);
	ATOMIC_SET_OFFSET_OF(&table->head, new_layer);
	return 0;
}

// Free every layer in the stale chain.
//
// Called internally by fwtable_trim_stale_cp (to release the previous
// generation) and usable standalone during table destruction.
static inline void
fwtable_free_stale(fwtable_t *table, struct memory_context *ctx) {
	fwmap_t *layer = ADDR_OF(&table->stale);
	while (layer) {
		fwmap_t *next = (fwmap_t *)ADDR_OF(&layer->next);
		fwmap_free(layer, ctx);
		layer = next;
	}
	SET_OFFSET_OF(&table->stale, NULL);
}

// Reclaim an expired tail layer from the active chain.
//
// First frees the stale layers reclaimed last call, then, if the
// oldest layer past the head is expired, atomically unlinks it to
// table->stale.  Only the tail is trimmed, so middle next links stay
// intact and a reader walking from the head always reaches the current
// tail; the head is never trimmed.
//
// Trimmed layers are freed on the next call, so a dataplane reader
// must not span two consecutive trims — the caller bounds reader
// duration below one trim interval.  Returns 0.
static inline int
fwtable_trim_stale_cp(
	fwtable_t *table, struct memory_context *ctx, uint64_t now
) {
	fwtable_free_stale(table, ctx);

	fwmap_t *head = ADDR_OF(&table->head);
	if (!head || !head->next) {
		return 0;
	}

	fwmap_t *prev = head;
	fwmap_t *tail = (fwmap_t *)ADDR_OF(&head->next);
	while (tail->next) {
		prev = tail;
		tail = (fwmap_t *)ADDR_OF(&tail->next);
	}

	if (fwmap_max_deadline(tail) > now) {
		return 0;
	}

	ATOMIC_SET_OFFSET_OF(&prev->next, NULL);
	fwmap_t *stale_head = ADDR_OF(&table->stale);
	ATOMIC_SET_OFFSET_OF(&tail->next, stale_head);
	SET_OFFSET_OF(&table->stale, tail);

	return 0;
}

// Search a layer chain newest-to-oldest for key.
//
// start is the first layer to examine (the table head for a top-level
// lookup, or the second layer when called from fwtable_insert which
// has already probed the active layer).
//
// If the key lives in the first (potentially read-write) layer the
// bucket read-lock is returned through *lock.  Entries past their
// deadline are treated as absent.  Returns the key index on hit or -1
// on miss.  When deadline is non-NULL it receives the entry's expiry.
// *from_stale is false for a hit in start itself, true for a deeper layer.
static inline int64_t
fwtable_lookup_internal(
	fwmap_t *start,
	uint64_t now,
	const void *key,
	void **value,
	rwlock_t **lock,
	uint64_t *deadline,
	bool *from_stale
) {
	int64_t result = fwmap_get_value_and_deadline(
		start, now, key, value, lock, deadline
	);
	if (result >= 0) {
		*from_stale = false;
		return result;
	}

	*from_stale = true;
	fwmap_t *layer = (fwmap_t *)ATOMIC_ADDR_OF(&start->next);
	if (!layer) {
		return -1;
	}
	result = fwmap_get_value_and_deadline(
		layer, now, key, value, lock, deadline
	);
	if (result >= 0) {
		return result;
	}

	// Release the first-layer lock before walking read-only layers.
	if (lock && *lock) {
		rwlock_read_unlock(*lock);
		*lock = NULL;
	}

	while (true) {
		fwmap_t *next = (fwmap_t *)ATOMIC_ADDR_OF(&layer->next);
		if (!next) {
			break;
		}
		layer = next;
		result = fwmap_get_value_and_deadline(
			layer, now, key, value, NULL, deadline
		);
		if (result >= 0) {
			return result;
		}
	}

	return -1;
}

// Read-only lookup across all layers of the table.
//
// Returns the key index on hit or -1 on miss.  *from_stale is false
// when the hit is in the active layer, true when it comes from a stale
// layer.  When lock is non-NULL a read-lock on the active-layer bucket
// is returned and the caller must release it.
static inline int64_t
fwtable_lookup(
	fwtable_t *table,
	uint64_t now,
	const void *key,
	void **value,
	rwlock_t **lock,
	bool *from_stale
) {
	fwmap_t *head = ATOMIC_ADDR_OF(&table->head);
	if (!head) {
		*from_stale = false;
		return -1;
	}
	return fwtable_lookup_internal(
		head, now, key, value, lock, NULL, from_stale
	);
}

// Read-only lookup that also returns the entry deadline.
//
// Same contract as fwtable_lookup, with the addition of *deadline
// receiving the entry's expiry timestamp on hit.
static inline int64_t
fwtable_lookup_with_deadline(
	fwtable_t *table,
	uint64_t now,
	const void *key,
	void **value,
	rwlock_t **lock,
	uint64_t *deadline,
	bool *from_stale
) {
	fwmap_t *head = ATOMIC_ADDR_OF(&table->head);
	if (!head) {
		*from_stale = false;
		return -1;
	}
	return fwtable_lookup_internal(
		head, now, key, value, lock, deadline, from_stale
	);
}

// Insert or update key in the active layer, promoting any existing
// value found in a stale layer.
//
// Locates the key in the head layer (allocating a new slot if needed),
// then, for a fresh slot, searches lower layers for a prior value to
// merge via the promote_value callback.  The value is then written
// through the update_value callback.
//
// Returns the key index on success or -1 if the active layer is full
// or the table is empty.
static inline int64_t
fwtable_insert(
	fwtable_t *table,
	uint16_t worker_idx,
	uint64_t now,
	uint64_t ttl,
	const void *key,
	const void *value,
	rwlock_t **lock
) {
	fwmap_t *active = ATOMIC_ADDR_OF(&table->head);
	if (!active) {
		return -1;
	}

	fwmap_copy_key_fn_t copy_key_fn = (fwmap_copy_key_fn_t
	)fwmap_func_registry[active->copy_key_fn_id];
	fwmap_update_value_fn_t update_value_fn = (fwmap_update_value_fn_t
	)fwmap_func_registry[active->update_value_fn_id];
	fwmap_promote_value_fn_t promote_value_fn = (fwmap_promote_value_fn_t
	)fwmap_func_registry[active->promote_value_fn_id];

	fwmap_entry_t entry =
		fwmap_entry(active, worker_idx, now, ttl, key, lock);
	if (!entry.key) {
		return -1;
	}

	if (entry.empty) {
		copy_key_fn(entry.key, key, active->key_size);

		fwmap_t *next_layer = (fwmap_t *)ATOMIC_ADDR_OF(&active->next);
		if (next_layer) {
			rwlock_t *read_lock = NULL;
			void *old_value = NULL;
			bool from_stale = false;

			int64_t found = fwtable_lookup_internal(
				next_layer,
				now,
				key,
				&old_value,
				&read_lock,
				NULL,
				&from_stale
			);
			if (found >= 0) {
				promote_value_fn(
					entry.value,
					value,
					old_value,
					active->value_size
				);
				if (read_lock) {
					rwlock_read_unlock(read_lock);
				}
				return (int64_t)entry.idx;
			}
			if (read_lock) {
				rwlock_read_unlock(read_lock);
			}
		}
	}

	update_value_fn(entry.value, value, entry.empty, active->value_size);
	return (int64_t)entry.idx;
}
