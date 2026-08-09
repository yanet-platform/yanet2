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
// through fwmap_t::next is progressively older and immutable.  The
// control plane grows the chain with fwtable_insert_layer_cp and
// reclaims expired layers with fwtable_trim_stale_cp.  The dataplane
// searches the chain with fwtable_lookup and writes through
// fwtable_insert.
//
// Trimmed layers are moved to the stale chain (chained via
// fwmap_t::next) and freed on the next trim call, giving dataplane
// readers one trim cycle to quiesce.
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
	SET_OFFSET_OF(&table->head, new_layer);
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

// Reclaim expired layers from the active chain.
//
// First frees the previous generation of stale layers (those trimmed
// in the last call).  Then walks the active chain past the head and
// atomically unlinks every layer whose max deadline is at or before
// now, moving each into table->stale.  The active head is never
// trimmed.
//
// Returns 0 on success or -1 on error (partial trim results are
// preserved).
static inline int
fwtable_trim_stale_cp(
	fwtable_t *table, struct memory_context *ctx, uint64_t now
) {
	fwtable_free_stale(table, ctx);

	fwmap_t *head = ADDR_OF(&table->head);
	if (!head) {
		return 0;
	}

	fwmap_t **prev_next = (fwmap_t **)&head->next;
	fwmap_t *layer = ADDR_OF(prev_next);

	while (layer) {
		if (fwmap_max_deadline(layer) <= now) {
			fwmap_t *next = (fwmap_t *)ADDR_OF(&layer->next);
			ATOMIC_SET_OFFSET_OF(prev_next, next);

			fwmap_t *stale_head = ADDR_OF(&table->stale);
			SET_OFFSET_OF(&layer->next, stale_head);
			SET_OFFSET_OF(&table->stale, layer);

			layer = next;
		} else {
			prev_next = (fwmap_t **)&layer->next;
			layer = (fwmap_t *)ADDR_OF(prev_next);
		}
	}

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
// *from_stale is false for a first-layer hit, true otherwise.
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
	if (!start->next) {
		return -1;
	}

	fwmap_t *layer = (fwmap_t *)ADDR_OF(&start->next);
	result = fwmap_get_value_and_deadline(
		layer, now, key, value, NULL, deadline
	);
	if (result >= 0) {
		return result;
	}

	// Release the first-layer lock before walking read-only layers.
	if (lock && *lock) {
		rwlock_read_unlock(*lock);
		*lock = NULL;
	}

	while (layer->next) {
		layer = (fwmap_t *)ADDR_OF(&layer->next);
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
	fwmap_t *head = ADDR_OF(&table->head);
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
	fwmap_t *head = ADDR_OF(&table->head);
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
	fwmap_t *active = ADDR_OF(&table->head);
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

		if (active->next) {
			fwmap_t *next_layer = (fwmap_t *)ADDR_OF(&active->next);
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
