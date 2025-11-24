#pragma once

#include <stdbool.h>
#include <stdio.h>

#include "common/container_of.h"
#include "common/memory.h"

struct registry_item {
	uint64_t refcnt;
	uint64_t index;
};

static inline void
registry_item_init(struct registry_item *item) {
	item->refcnt = 0;
}

static inline void
registry_item_ref(struct registry_item *item) {
	item->refcnt += 1;
}

typedef void (*registry_item_free_func)(struct registry_item *item, void *data);

static inline void
registry_item_unref(
	struct registry_item *item,
	registry_item_free_func free_func,
	void *free_func_data
) {
	item->refcnt -= 1;
	if (!item->refcnt)
		free_func(item, free_func_data);
}

struct registry {
	struct memory_context *memory_context;
	uint64_t capacity;
	struct registry_item **items;
};

static inline struct registry_item *
registry_get(struct registry *registry, uint64_t idx) {
#ifdef REGISTRY_SANITIZE
	if (idx >= registry->capacity)
		return NULL;
#endif
	return ADDR_OF(ADDR_OF(&registry->items) + idx);
}

static inline void
registry_set(
	struct registry *registry, uint64_t idx, struct registry_item *item
) {
#ifdef REGISTRY_SANITIZE
	if (idx >= registry->capacity)
		return;
#endif
	SET_OFFSET_OF(ADDR_OF(&registry->items) + idx, item);
}

static inline int
registry_init(
	struct memory_context *memory_context,
	struct registry *registry,
	uint64_t capacity
) {
	SET_OFFSET_OF(&registry->memory_context, memory_context);
	registry->capacity = capacity;

	struct registry_item **items = (struct registry_item **)memory_balloc(
		memory_context, sizeof(struct registry_item *) * capacity
	);
	if (items == NULL) {
		return -1;
	}
	SET_OFFSET_OF(&registry->items, items);

	for (uint64_t idx = 0; idx < capacity; ++idx) {
		registry_set(registry, idx, NULL);
	}

	return 0;
}

static inline void
registry_destroy(
	struct registry *registry,
	registry_item_free_func item_free_func,
	void *item_free_func_data
) {
	struct memory_context *memory_context =
		ADDR_OF(&registry->memory_context);

	for (uint64_t idx = 0; idx < registry->capacity; ++idx) {
		struct registry_item *item = registry_get(registry, idx);
		if (item == NULL)
			continue;

		registry_item_unref(item, item_free_func, item_free_func_data);
	}

	memory_bfree(
		memory_context,
		ADDR_OF(&registry->items),
		sizeof(struct registry_item *) * registry->capacity
	);
}

static inline int
registry_copy(
	struct memory_context *memory_context,
	struct registry *new_registry,
	struct registry *old_registry
) {
	if (registry_init(
		    memory_context, new_registry, old_registry->capacity
	    )) {
		return -1;
	}

	for (uint64_t idx = 0; idx < old_registry->capacity; ++idx) {
		struct registry_item *item = registry_get(old_registry, idx);

		if (item != NULL) {
			registry_item_ref(item);
		}

		registry_set(new_registry, idx, item);
	}

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
	for (uint64_t idx = old_capacity; idx < new_capacity; ++idx) {
		SET_OFFSET_OF(new_items + idx, NULL);
	}

	memory_bfree(
		memory_context,
		old_items,
		sizeof(struct registry_item *) * old_capacity
	);

	registry->capacity = new_capacity;

	return 0;
}

typedef int (*registry_item_cmp_func)(
	const struct registry_item *item, const void *data
);

static inline int
registry_lookup(
	struct registry *registry,
	registry_item_cmp_func cmp_func,
	const void *cmp_data,
	uint64_t *index
) {
	for (uint64_t idx = 0; idx < registry->capacity; ++idx) {
		struct registry_item *item = registry_get(registry, idx);
		if (item == NULL)
			continue;
		if (!cmp_func(item, cmp_data)) {
			*index = idx;
			return 0;
		}
	}

	return -1;
}

static inline int
registry_get_unused_index(struct registry *registry, uint64_t *index) {
	*index = 0;
	while (*index < registry->capacity) {
		if (registry_get(registry, *index) == NULL) {
			return 0;
		}
		*index += 1;
	}

	return registry_extend(registry);
}

static inline int
registry_insert(struct registry *registry, struct registry_item *new_item) {
	uint64_t index;
	if (registry_get_unused_index(registry, &index)) {
		return -1;
	}

	registry_set(registry, index, new_item);

	return 0;
}

static inline int
registry_replace(
	struct registry *registry,
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
	if (registry_lookup(registry, cmp_func, cmp_func_data, &index)) {
		if (new_item == NULL) {
			// Delete not existent item
			return -1;
		}

		if (registry_get_unused_index(registry, &index)) {
			return -1;
		}
	}

	struct registry_item *old_item = registry_get(registry, index);
	if (new_item != NULL) {
		registry_item_ref(new_item);
		new_item->index = index;
	}

	registry_set(registry, index, new_item);

	if (old_item != NULL) {
		registry_item_unref(
			old_item, item_free_func, item_free_func_data
		);
	}

	return 0;
}
