#include "common/memory.h"
#include "common/memory_address.h"
#include "fwmap.h"
#include <stdatomic.h>
#include <unistd.h>

typedef struct layermap_list {
	fwmap_t *layer;
	struct layermap_list *next;
} layermap_list_t;

// Checks if a layer is outdated based on its deadline.
static inline bool
layermap_is_layer_outdated(const fwmap_t *layer, uint64_t now) {
	// Safe to check without locks since we only examine read-only layers
	// with no ongoing writes.
	return fwmap_max_deadline(layer) <= now;
}

static inline int
layermap_trim_stale_layers_cp(
	fwmap_t **active_layer_offset,
	struct memory_context *ctx,
	uint64_t now,
	layermap_list_t **outdated_layers
) {
	fwmap_t *active_layer = ADDR_OF(active_layer_offset);
	// Start from the layer after the active layer
	fwmap_t **prev_next = (fwmap_t **)&active_layer->next;
	fwmap_t *layer = ADDR_OF(prev_next);

	while (layer) {
		// Check if all workers have seen this layer as sealed
		bool is_sealed = (layer->sealed_count >= layer->worker_count);

		if (is_sealed && layermap_is_layer_outdated(layer, now)) {
			// Unlink the outdated layer
			fwmap_t *next_layer = (fwmap_t *)ADDR_OF(&layer->next);
			SET_OFFSET_OF(prev_next, next_layer);
			__atomic_thread_fence(__ATOMIC_RELEASE);

			// Add to outdated layers list
			layermap_list_t *node =
				memory_balloc(ctx, sizeof(layermap_list_t));
			if (!node) {
				return -1;
			}
			SET_OFFSET_OF(&node->layer, layer);
			SET_OFFSET_OF(&node->next, *outdated_layers);
			*outdated_layers = node;

			// Move to next layer without updating prev_next
			layer = next_layer;
		} else {
			// Move to next layer and update prev_next
			prev_next = (fwmap_t **)&layer->next;
			layer = (fwmap_t *)ADDR_OF(prev_next);
		}
	}

	return 0;
}

static inline int
layermap_insert_new_layer_cp(
	fwmap_t **active_layer_offset,
	fwmap_config_t *config,
	struct memory_context *ctx
) {

	// Allocate new layer
	fwmap_t *new_layer = fwmap_new(config, ctx);
	if (!new_layer) {
		return -1;
	}

	fwmap_t *active_layer = ADDR_OF(active_layer_offset);
	// Link the active layer as next to the new layer
	SET_OFFSET_OF(&new_layer->next, active_layer);
	// Set the new layer as active
	SET_OFFSET_OF(active_layer_offset, new_layer);

	return 0;
}

// Internal function: Searches for a key across all layers, from newest to
// oldest.
static inline int64_t
layermap_get_internal(
	fwmap_t *active_layer,
	uint16_t worker_idx,
	uint64_t now,
	const void *key,
	void **value,
	rwlock_t **lock,
	uint64_t *deadline,
	bool *value_from_stale_layer
) {
	(void)worker_idx;

	// Lock required for safe access to the active layer (which handles
	// writes).
	int64_t result = fwmap_get_value_and_deadline(
		active_layer, now, key, value, lock, deadline
	);
	if (result >= 0) {
		*value_from_stale_layer = false;
		return result;
	}
	*value_from_stale_layer = true;

	if (lock && *lock) {
		// Tradeoff: holding the lock ensures the most recent value but
		// slows down the map. Releasing it allows concurrent writes but
		// may return stale values from read-only layers if another
		// thread inserts into the active layer in parallel.
		rwlock_read_unlock(*lock);
		*lock = NULL;
	}
	if (!active_layer->next) {
		return -1; // Key not found in any layer
	}

	// Iterate over read-only layers. These may still have in-progress
	// writes from when they were active. Sealed layers can be accessed
	// without locks; unsealed layers require locking and sealing marks.
	fwmap_t *layer = (fwmap_t *)ADDR_OF(&active_layer->next);
	result = fwmap_get_value_and_deadline(
		layer, now, key, value, lock, deadline
	);

	if (result >= 0) {
		return result;
	}

	if (lock && *lock) {
		// FIXME: check if sealed and use lock only if not
		rwlock_read_unlock(*lock);
		*lock = NULL;
	}

	while (layer->next) {
		// Once a layer's next pointer is set, subsequent layers cannot
		// change their next pointers to invalid maps, so atomic access
		// is not required.
		layer = (fwmap_t *)ADDR_OF(&layer->next);
		result = fwmap_get_value_and_deadline(
			layer, now, key, value, NULL, deadline
		);
		if (result >= 0) {
			return result;
		}
	}

	return -1; // Key not found in any layer
}

// Searches for a key across all layers, from newest to oldest.
static inline int64_t
layermap_get(
	fwmap_t *active_layer,
	uint16_t worker_idx,
	uint64_t now,
	const void *key,
	void **value,
	rwlock_t **lock,
	bool *value_from_stale_layer
) {
	return layermap_get_internal(
		active_layer,
		worker_idx,
		now,
		key,
		value,
		lock,
		NULL,
		value_from_stale_layer
	);
}

// Searches for a key across all layers and returns the deadline.
static inline int64_t
layermap_get_value_and_deadline(
	fwmap_t *active_layer,
	uint16_t worker_idx,
	uint64_t now,
	const void *key,
	void **value,
	rwlock_t **lock,
	uint64_t *deadline,
	bool *value_from_stale_layer
) {
	return layermap_get_internal(
		active_layer,
		worker_idx,
		now,
		key,
		value,
		lock,
		deadline,
		value_from_stale_layer
	);
}

// Inserts or updates a key-value pair in the active layer.
static inline int64_t
layermap_put(
	fwmap_t *active_layer,
	uint16_t worker_idx,
	uint64_t now,
	uint64_t ttl,
	const void *key,
	const void *value,
	rwlock_t **lock
) {
	fwmap_copy_key_fn_t copy_key_fn = (fwmap_copy_key_fn_t
	)fwmap_func_registry[active_layer->copy_key_fn_id];
	fwmap_copy_value_fn_t copy_value_fn = (fwmap_copy_value_fn_t
	)fwmap_func_registry[active_layer->copy_value_fn_id];
	fwmap_merge_value_fn_t merge_value_fn = (fwmap_merge_value_fn_t
	)fwmap_func_registry[active_layer->merge_value_fn_id];

	fwmap_entry_t entry =
		fwmap_entry(active_layer, worker_idx, now, ttl, key, lock);
	if (!entry.key) {
		return -1;
	}
	if (entry.empty) {
		copy_key_fn(entry.key, key, active_layer->key_size);

		// Check if there is a next layer to merge from
		if (active_layer->next) {
			rwlock_t *read_lock = NULL;
			void *old_value;
			bool value_from_stale;
			fwmap_t *next_layer =
				(fwmap_t *)ADDR_OF(&active_layer->next);
			// FIXME: check if layer is sealed; if so, get without
			// lock
			int64_t result = layermap_get(
				next_layer,
				worker_idx,
				now,
				key,
				&old_value,
				&read_lock,
				&value_from_stale
			);
			if (result >= 0) {
				merge_value_fn(
					entry.value,
					value,
					old_value,
					active_layer->value_size
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

	copy_value_fn(entry.value, value, active_layer->value_size);
	return (int64_t)entry.idx;
}
