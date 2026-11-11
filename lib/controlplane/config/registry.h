#pragma once

#include <stddef.h>
#include <stdint.h>

#include "common/hash_index.h"
#include "common/memory.h"
#include "common/str_index.h"

#include "lib/controlplane/config/config_lock.h"

#define REGISTRY_HASH_SEED 0x9e3779b9

struct registry_item {
	// Reference count of live registry slots holding this item.
	//
	// Gains only ever happen under the configuration lock; drops and
	// reads are relaxed atomics, because a retired generation's
	// registries are released by the last pinning reader without that
	// lock. An owner reading the count under the lock may therefore see
	// a stale high value — one that a racing drop has not lowered yet —
	// which can only postpone an owner's destroy attempt, never free an
	// item another registry still holds.
	uint64_t refcnt;
};

static inline void
registry_item_init(struct registry_item *item) {
	item->refcnt = 0;
}

// Take one reference for a registry slot; like every registry mutation
// this must run under the control-plane configuration lock
// (cp_config_lock) of the owning configuration.
//
// Checked in debug builds against the owner: a caller outside that
// lock loses updates and leaks the counted item. An owner of NULL
// exempts ownerless test registries.
static inline void
registry_item_ref(struct registry_item *item, const struct cp_config *owner) {
	cp_config_assert_locked(owner);
	// The bump is itself atomic: pin-dropping releases run without the
	// lock, and a plain increment interleaving with them could lose a
	// decrement.
	__atomic_fetch_add(&item->refcnt, 1, __ATOMIC_RELAXED);
}

typedef void (*registry_item_free_func)(struct registry_item *item, void *data);

// Drop one reference.
//
// Called under the configuration lock by registry mutations, and
// without it by a retired generation's teardown, so the count moves
// with an atomic RMW. The subtraction releases the caller's prior
// accesses to the item, and the subtraction that observes zero
// acquires every earlier releaser's publication — the release
// sequence the chained RMWs form carries each one to the final
// subtraction — so a destroy callback runs ordered after the last
// holder's reads of the item instead of racing them on a weakly
// ordered target.
//
// Without a destroy callback the zero transition is a no-op: the item
// stays alive, dangling, and only its owner may destroy it.
//
// A destroy callback may run on either path, including the unlocked
// final release of a retired generation, so it must only perform
// teardown that is safe without the configuration lock — memory return
// through the internally locked allocator, never mutation of state the
// lock guards.
static inline void
registry_item_unref(
	struct registry_item *item,
	const struct cp_config *owner,
	registry_item_free_func free_func,
	void *free_func_data
) {
	(void)owner;
	uint64_t remaining =
		__atomic_sub_fetch(&item->refcnt, 1, __ATOMIC_ACQ_REL);
	if (!remaining && free_func != NULL) {
		free_func(item, free_func_data);
	}
}

typedef int (*registry_item_cmp_func)(
	const struct registry_item *item, const void *data
);

// The key shape a registry's hash side-index serves.
//
// A registry may be searched with several comparison shapes; only the
// canonical one drives the hash functions, and only lookups and
// replacements using exactly that comparison go through the index,
// every other shape keeps the linear scan. The caller supplies the key
// set on every indexed operation, since a shared registry cannot hold
// it: the callbacks live in each binary's static storage. A registry
// is driven with one key set, or none; mixing sets is a caller bug.
struct registry_keys {
	registry_item_cmp_func cmp;
	// Hash of a lookup key in the canonical shape.
	uint64_t (*key_hash)(const void *key);
	// Hash of an item's own key, agreeing with the lookup hash.
	uint64_t (*item_hash)(const struct registry_item *item);
};

// Hash of one NUL-terminated key field.
static inline uint64_t
registry_hash_name(const char *name, size_t limit) {
	return str_index_fnv1a(name, strnlen(name, limit), REGISTRY_HASH_SEED);
}

// Hash of a two-field key: the fields fold in order, so both influence
// the digest and lookup comparison, not the hash, decides equality.
static inline uint64_t
registry_hash_name_pair(
	const char *first,
	size_t first_limit,
	const char *second,
	size_t second_limit
) {
	uint64_t hash = str_index_fnv1a(
		first, strnlen(first, first_limit), REGISTRY_HASH_SEED
	);
	return str_index_fnv1a(
		second, strnlen(second, second_limit), (uint32_t)hash
	);
}

// Fold the 64-bit key hash into the 32 bits the side index probes with.
static inline uint32_t
registry_hash_fold(uint64_t hash) {
	return (uint32_t)(hash ^ (hash >> 32));
}

struct registry {
	struct memory_context *memory_context;
	// Configuration whose lock must be held for every reference-count
	// mutation; NULL exempts ownerless test registries.
	struct cp_config *owner;
	uint64_t capacity;
	struct registry_item **items;
	// Head of the free chain threaded through the empty slots.
	struct registry_item *free_list;
	struct hash_index key_index;
};

// A free slot's own word carries the address of the next free slot,
// the same intrusive chain the block allocator threads through freed
// blocks; the head lives in the registry.
//
// A link resolves back inside the items block, and a live item never
// does — the block allocator's blocks never overlap — so the accessor
// below classifies link words as free without any extra marking.
static inline struct registry_item *
registry_get(struct registry *registry, uint64_t idx) {
#ifdef REGISTRY_SANITIZE
	if (idx >= registry->capacity) {
		return NULL;
	}
#endif
	struct registry_item **items = ADDR_OF(&registry->items);
	struct registry_item *item = ADDR_OF(items + idx);
	if (item == NULL) {
		return NULL;
	}
	if ((uintptr_t)item >= (uintptr_t)items &&
	    (uintptr_t)item < (uintptr_t)(items + registry->capacity)) {
		return NULL;
	}
	return item;
}

static inline void
registry_set(
	struct registry *registry, uint64_t idx, struct registry_item *item
) {
#ifdef REGISTRY_SANITIZE
	if (idx >= registry->capacity) {
		return;
	}
#endif
	SET_OFFSET_OF(ADDR_OF(&registry->items) + idx, item);
}

// Pop the first free slot, or report the registry full.
static inline int
registry_free_pop(struct registry *registry, uint64_t *index) {
	struct registry_item **items = ADDR_OF(&registry->items);
	struct registry_item *head = ADDR_OF(&registry->free_list);
	if (head == NULL) {
		return -1;
	}

	struct registry_item *next = ADDR_OF((struct registry_item **)head);
	SET_OFFSET_OF(&registry->free_list, next);
	*index = ((uintptr_t)head - (uintptr_t)items) / sizeof(*items);
	return 0;
}

// Return a slot to the head of the free chain.
static inline void
registry_free_push(struct registry *registry, uint64_t index) {
	struct registry_item **slot = ADDR_OF(&registry->items) + index;
	struct registry_item *head = ADDR_OF(&registry->free_list);
	SET_OFFSET_OF((struct registry_item **)slot, head);
	SET_OFFSET_OF(&registry->free_list, (struct registry_item *)slot);
}

// Equality probe of the side index: a bucket payload is an items-array
// slot, accepted when the item in it matches the lookup key.
struct registry_index_probe {
	struct registry *registry;
	registry_item_cmp_func cmp_func;
	const void *cmp_data;
};

static inline int
registry_index_probe_eq(uint32_t value, const void *data) {
	const struct registry_index_probe *probe =
		(const struct registry_index_probe *)data;

	struct registry_item *item = registry_get(probe->registry, value);
	if (item == NULL) {
		return 1;
	}

	return probe->cmp_func(item, probe->cmp_data);
}

// Catch the side index up to the items capacity before an insertion.
//
// Returns 0 with the index aligned, or -1 leaving the registry sound:
// an index behind the items array still resolves every live item, so
// the caller may simply fail its own mutation and retry.
static inline int
registry_index_ensure(struct registry *registry) {
	if ((uint64_t)registry->key_index.capacity >= registry->capacity) {
		return 0;
	}

	return hash_index_rehash(
		&registry->key_index, (uint32_t)registry->capacity
	);
}

#ifdef REGISTRY_SANITIZE
#include "lib/controlplane/config/registry_sanitize.h"
#endif

static inline int
registry_init(
	struct memory_context *memory_context,
	struct cp_config *owner,
	struct registry *registry,
	uint64_t capacity
) {
	if (capacity > UINT32_MAX) {
		return -1;
	}

	SET_OFFSET_OF(&registry->memory_context, memory_context);
	SET_OFFSET_OF(&registry->owner, owner);
	registry->capacity = capacity;

	struct registry_item **items = (struct registry_item **)memory_balloc(
		memory_context, sizeof(struct registry_item *) * capacity
	);
	if (items == NULL) {
		return -1;
	}
	SET_OFFSET_OF(&registry->items, items);
	SET_OFFSET_OF(&registry->free_list, NULL);

	// Descending pushes leave the chain ascending, so a fresh registry
	// hands out slots from the front.
	for (uint64_t idx = capacity; idx-- > 0;) {
		registry_free_push(registry, idx);
	}

	if (hash_index_init(
		    &registry->key_index, memory_context, (uint32_t)capacity
	    )) {
		SET_OFFSET_OF(&registry->items, NULL);
		memory_bfree(
			memory_context,
			items,
			sizeof(struct registry_item *) * capacity
		);
		return -1;
	}

	return 0;
}

// Free a registry's storage without touching its items, for teardown
// paths that release every item reference themselves before the storage
// goes.
//
// Idempotent on zero-init, like registry_fini below.
static inline void
registry_discard(struct registry *registry) {
	if (ADDR_OF(&registry->items) == NULL) {
		return;
	}

	hash_index_fini(&registry->key_index);

	memory_bfree(
		ADDR_OF(&registry->memory_context),
		ADDR_OF(&registry->items),
		sizeof(struct registry_item *) * registry->capacity
	);

	memset(registry, 0, sizeof(*registry));
}

// Release all items in the registry, free the items array, and zero the struct.
//
// Idempotent on zero-init: safe to call on a registry that was never
// initialized or has already been finalized.
static inline void
registry_fini(
	struct registry *registry,
	registry_item_free_func item_free_func,
	void *item_free_func_data
) {
	if (ADDR_OF(&registry->items) == NULL) {
		return;
	}

	struct cp_config *owner = ADDR_OF(&registry->owner);

	for (uint64_t idx = 0; idx < registry->capacity; ++idx) {
		struct registry_item *item = registry_get(registry, idx);
		if (item == NULL) {
			continue;
		}

		registry_item_unref(
			item, owner, item_free_func, item_free_func_data
		);
	}

	registry_discard(registry);
}

static inline int
registry_copy(
	struct memory_context *memory_context,
	const struct registry_keys *keys,
	struct registry *new_registry,
	struct registry *old_registry
) {
	if (registry_init(
		    memory_context,
		    ADDR_OF(&old_registry->owner),
		    new_registry,
		    old_registry->capacity
	    )) {
		return -1;
	}

	for (uint64_t idx = 0; idx < old_registry->capacity; ++idx) {
		struct registry_item *item = registry_get(old_registry, idx);

		if (item != NULL) {
			registry_item_ref(item, ADDR_OF(&old_registry->owner));
		}

		registry_set(new_registry, idx, item);
	}

	// The placements above trampled the chain built at init, so the
	// free chain is rethreaded over the surviving holes; descending
	// pushes keep the copy handing out its lowest hole first, as its
	// predecessor did.
	SET_OFFSET_OF(&new_registry->free_list, NULL);
	for (uint64_t idx = new_registry->capacity; idx-- > 0;) {
		if (registry_get(new_registry, idx) == NULL) {
			registry_free_push(new_registry, idx);
		}
	}

	if (keys != NULL) {
		for (uint64_t idx = 0; idx < old_registry->capacity; ++idx) {
			struct registry_item *item =
				registry_get(new_registry, idx);
			if (item == NULL) {
				continue;
			}

			// Live slots never exceed the items capacity the
			// index above was sized to, so the insert cannot
			// find it full.
			hash_index_insert(
				&new_registry->key_index,
				registry_hash_fold(keys->item_hash(item)),
				(uint32_t)idx
			);
		}
	}

#ifdef REGISTRY_SANITIZE
	registry_sanitize_index(new_registry, keys);
#endif

	return 0;
}

static inline int
registry_extend(struct registry *registry) {
	struct memory_context *memory_context =
		ADDR_OF(&registry->memory_context);

	uint64_t old_capacity = registry->capacity;
	uint64_t new_capacity = old_capacity * 2 + !old_capacity;

	struct registry_item **old_items = ADDR_OF(&registry->items);

	struct registry_item **new_items =
		(struct registry_item **)memory_balloc(
			memory_context,
			sizeof(struct registry_item **) * new_capacity
		);
	if (new_items == NULL) {
		return -1;
	}

	for (uint64_t idx = 0; idx < old_capacity; ++idx) {
		SET_OFFSET_OF(new_items + idx, ADDR_OF(old_items + idx));
	}

	SET_OFFSET_OF(&registry->items, new_items);

	// Reached only with the free chain empty — every copied word is a
	// live item offset, never a link into the array being freed. The
	// fresh tail joins the chain, so the next acquisition takes the
	// first new slot.
	for (uint64_t idx = new_capacity; idx-- > old_capacity;) {
		registry_free_push(registry, idx);
	}

	memory_bfree(
		memory_context,
		old_items,
		sizeof(struct registry_item *) * old_capacity
	);

	registry->capacity = new_capacity;

	return 0;
}

// Resolve a key through the side index when the comparison is the
// indexed key shape, and through the linear scan otherwise.
static inline int
registry_lookup(
	struct registry *registry,
	const struct registry_keys *keys,
	registry_item_cmp_func cmp_func,
	const void *cmp_data,
	uint64_t *index
) {
	if (keys != NULL && cmp_func == keys->cmp) {
		struct registry_index_probe probe = {
			.registry = registry,
			.cmp_func = cmp_func,
			.cmp_data = cmp_data,
		};

		uint32_t found = hash_index_lookup(
			&registry->key_index,
			registry_hash_fold(keys->key_hash(cmp_data)),
			registry_index_probe_eq,
			&probe
		);
		if (found == HASH_INDEX_INVALID) {
			return -1;
		}

		*index = found;
		return 0;
	}

	for (uint64_t idx = 0; idx < registry->capacity; ++idx) {
		struct registry_item *item = registry_get(registry, idx);
		if (item == NULL) {
			continue;
		}
		if (!cmp_func(item, cmp_data)) {
			*index = idx;
			return 0;
		}
	}

	return -1;
}

// Take a slot for an item: the first free slot, or the first slot of a
// doubled array when none is free.
static inline int
registry_acquire_slot(struct registry *registry, uint64_t *index) {
	if (!registry_free_pop(registry, index)) {
		return 0;
	}
	if (registry_extend(registry)) {
		return -1;
	}
	return registry_free_pop(registry, index);
}

static inline int
registry_insert(
	struct registry *registry,
	const struct registry_keys *keys,
	struct registry_item *new_item
) {
	uint64_t index;
	if (registry_acquire_slot(registry, &index)) {
		return -1;
	}
	if (keys != NULL && registry_index_ensure(registry)) {
		// The taken slot returns to the chain, so a failed mutation
		// leaves the registry exactly as it was.
		registry_free_push(registry, index);
		return -1;
	}

	registry_set(registry, index, new_item);
	if (keys != NULL) {
		// The ensure above aligned the index with the items array,
		// so the entry insert cannot find it full.
		hash_index_insert(
			&registry->key_index,
			registry_hash_fold(keys->item_hash(new_item)),
			(uint32_t)index
		);
	}
	registry_item_ref(new_item, ADDR_OF(&registry->owner));

#ifdef REGISTRY_SANITIZE
	registry_sanitize_index(registry, keys);
#endif

	return 0;
}

static inline int
registry_replace(
	struct registry *registry,
	const struct registry_keys *keys,
	registry_item_cmp_func cmp_func,
	const void *cmp_func_data,
	struct registry_item *new_item,
	registry_item_free_func item_free_func,
	void *item_free_func_data
) {
#ifdef REGISTRY_SANITIZE
	if (new_item != NULL && cmp_func(new_item, cmp_func_data)) {
		return -1;
	}
#endif

	uint64_t index;
	if (registry_lookup(registry, keys, cmp_func, cmp_func_data, &index)) {
		if (new_item == NULL) {
			// Delete not existent item
			return -1;
		}

		if (registry_acquire_slot(registry, &index)) {
			return -1;
		}
		if (keys != NULL && registry_index_ensure(registry)) {
			// The taken slot returns to the chain, so a failed
			// mutation leaves the registry exactly as it was.
			registry_free_push(registry, index);
			return -1;
		}
	}

	struct registry_item *old_item = registry_get(registry, index);
	if (new_item != NULL) {
		registry_item_ref(new_item, ADDR_OF(&registry->owner));
	}

	if (new_item != NULL) {
		registry_set(registry, index, new_item);
	} else {
		registry_free_push(registry, index);
	}

	// The side index is maintained before the old item is released, so
	// its key stays readable while the slot's entry is swapped.
	if (keys != NULL) {
		if (old_item == NULL) {
			// A fresh slot: the ensure above aligned the index
			// with the items array, so the entry insert cannot
			// find it full.
			hash_index_insert(
				&registry->key_index,
				registry_hash_fold(keys->item_hash(new_item)),
				(uint32_t)index
			);
		} else {
			// The slot's entry keys the old item, so it is
			// dropped and, for a replacement, re-created under
			// the new item's key: a replaced item may hash to a
			// different probe chain than its predecessor.
			hash_index_remove(
				&registry->key_index,
				registry_hash_fold(keys->item_hash(old_item)),
				(uint32_t)index
			);
			if (new_item != NULL) {
				hash_index_insert(
					&registry->key_index,
					registry_hash_fold(
						keys->item_hash(new_item)
					),
					(uint32_t)index
				);
			}
		}
	}

	if (old_item != NULL) {
		registry_item_unref(
			old_item,
			ADDR_OF(&registry->owner),
			item_free_func,
			item_free_func_data
		);
	}

#ifdef REGISTRY_SANITIZE
	registry_sanitize_index(registry, keys);
#endif

	return 0;
}
