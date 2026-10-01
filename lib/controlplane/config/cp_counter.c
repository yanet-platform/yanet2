#include "cp_counter.h"

#include "common/hash_index.h"
#include "common/memory.h"

#include "common/memory_address.h"
#include "common/str_index.h"
#include "lib/counters/counters.h"
#include "lib/errors/errors.h"
#include <stdlib.h>
#include <string.h>

#define COUNTER_REGISTRY_PREALLOC 8

// The equality probe context: the query tag set plus the registry whose
// items the probed positions index into.
struct tag_set_lookup {
	struct cp_config_counter_storage_registry *registry;
	const struct counter_tag *tags;
	size_t tag_count;
};

static int
compare_tags(const void *left, const void *right) {
	const struct counter_tag *left_tag = (const struct counter_tag *)left;
	const struct counter_tag *right_tag = (const struct counter_tag *)right;
	return strcmp(left_tag->key, right_tag->key);
}

// Hash of a normalized tag set: keys and values folded in sorted order,
// seeded with the count so sets differing only in length land apart.
static uint32_t
tags_hash(const struct counter_tag *tags, size_t tag_count) {
	uint64_t hash = str_index_fnv1a(&tag_count, sizeof(tag_count), 0);
	for (size_t i = 0; i < tag_count; ++i) {
		hash = str_index_fnv1a(
			tags[i].key,
			strnlen(tags[i].key, COUNTER_TAG_KEY_LEN),
			(uint32_t)hash
		);
		hash = str_index_fnv1a(
			tags[i].value,
			strnlen(tags[i].value, COUNTER_TAG_VALUE_LEN),
			(uint32_t)hash
		);
	}
	return (uint32_t)hash;
}

// Equality of a probed item's tag set against the query's, both already
// normalized; zero only when every key and value matches.
static int
tag_set_equal(uint32_t item_idx, const void *data) {
	const struct tag_set_lookup *lookup = data;
	struct cp_counter_storage *items = ADDR_OF(&lookup->registry->items);
	struct cp_counter_storage *item = items + item_idx;
	if (item->tag_count != lookup->tag_count) {
		return 1;
	}
	for (size_t i = 0; i < lookup->tag_count; ++i) {
		if (strcmp(item->tags[i].key, lookup->tags[i].key) != 0 ||
		    strcmp(item->tags[i].value, lookup->tags[i].value) != 0) {
			return 1;
		}
	}
	return 0;
}

// Position of the item registered under exactly the passed normalized
// tag set, or HASH_INDEX_INVALID when no item carries it.
static uint32_t
tag_index_lookup(
	struct cp_config_counter_storage_registry *registry,
	const struct counter_tag *tags,
	size_t tag_count
) {
	struct tag_set_lookup lookup = {
		.registry = registry,
		.tags = tags,
		.tag_count = tag_count,
	};
	return hash_index_lookup(
		&registry->tag_index,
		tags_hash(tags, tag_count),
		tag_set_equal,
		&lookup
	);
}

// Rehash the index to new_capacity slots, in place.
//
// The index embeds self-relative offsets, so it cannot be rebuilt in a
// scratch struct and copied over. The old entries buffer is kept alive
// across the re-init and restored on failure, leaving the registry
// fully usable; it is freed only once every item is rehashed.
static int
tag_index_grow(
	struct cp_config_counter_storage_registry *registry, size_t new_capacity
) {
	struct hash_index *index = &registry->tag_index;
	uint32_t *old_entries = ADDR_OF(&index->entries);
	uint32_t old_capacity = index->capacity;
	uint32_t old_count = index->count;

	if (hash_index_init(
		    index,
		    ADDR_OF(&registry->memory_context),
		    (uint32_t)new_capacity
	    )) {
		SET_OFFSET_OF(&index->entries, old_entries);
		index->capacity = old_capacity;
		index->count = old_count;
		return -1;
	}

	struct cp_counter_storage *items = ADDR_OF(&registry->items);
	for (size_t i = 0; i < registry->count; ++i) {
		if (hash_index_insert(
			    index,
			    tags_hash(items[i].tags, items[i].tag_count),
			    (uint32_t)i
		    )) {
			uint32_t *new_entries = ADDR_OF(&index->entries);
			memory_bfree(
				ADDR_OF(&registry->memory_context),
				new_entries,
				sizeof(*new_entries) * new_capacity *
					HASH_INDEX_SPARSE_FACTOR
			);
			SET_OFFSET_OF(&index->entries, old_entries);
			index->capacity = old_capacity;
			index->count = old_count;
			return -1;
		}
	}

	if (old_entries != NULL) {
		memory_bfree(
			ADDR_OF(&registry->memory_context),
			old_entries,
			sizeof(*old_entries) * old_capacity *
				HASH_INDEX_SPARSE_FACTOR
		);
	}
	return 0;
}

int
cp_config_counter_storage_registry_init(
	struct memory_context *memory_context,
	struct cp_config_counter_storage_registry *registry,
	yanet_error **err
) {
	struct cp_counter_storage *items = memory_balloc(
		memory_context,
		sizeof(struct cp_counter_storage) * COUNTER_REGISTRY_PREALLOC
	);
	if (items == NULL) {
		yanet_error_add(
			err, "failed to initialize registry for counter storage"
		);
		return -1;
	}

	if (hash_index_init(
		    &registry->tag_index,
		    memory_context,
		    COUNTER_REGISTRY_PREALLOC
	    )) {
		memory_bfree(
			memory_context,
			items,
			sizeof(struct cp_counter_storage) *
				COUNTER_REGISTRY_PREALLOC
		);
		yanet_error_add(
			err, "failed to initialize registry for counter storage"
		);
		return -1;
	}

	SET_OFFSET_OF(&registry->items, items);
	registry->capacity = COUNTER_REGISTRY_PREALLOC;
	registry->count = 0;
	SET_OFFSET_OF(&registry->memory_context, memory_context);
	return 0;
}

static int
validate_tag(struct counter_tag *tag, bool predicate, yanet_error **err) {
	// The fixed-size fields cannot be NULL; the length checks only guard
	// against a struct built without NUL termination.
	if (strnlen(tag->key, COUNTER_TAG_KEY_LEN) == COUNTER_TAG_KEY_LEN) {
		yanet_error_add(
			err,
			"key length exceeds max %d",
			COUNTER_TAG_KEY_LEN - 1
		);
		return -1;
	}
	if (strnlen(tag->value, COUNTER_TAG_VALUE_LEN) ==
	    COUNTER_TAG_VALUE_LEN) {
		yanet_error_add(
			err,
			"value length exceeds max %d",
			COUNTER_TAG_VALUE_LEN - 1
		);
		return -1;
	}
	if (!predicate) {
		if (strcmp(tag->value, "") == 0) {
			yanet_error_add(
				err,
				"empty value is reserved for 'absent' predicate"
			);
			return -1;
		}
		if (strcmp(tag->value, "*") == 0) {
			yanet_error_add(
				err,
				"* value is reserved for 'present' predicate"
			);
			return -1;
		}
	}
	return 0;
}

static int
normalize_tags(
	struct counter_tag *tags,
	size_t tag_count,
	bool predicate,
	yanet_error **err
) {
	if (tag_count > MAX_TAG_COUNT) {
		yanet_error_add(err, "tag count exceeds max %d", MAX_TAG_COUNT);
		return -1;
	}

	for (size_t i = 0; i < tag_count; ++i) {
		if (validate_tag(tags + i, predicate, err) != 0) {
			yanet_error_add(err, "tag at index %zu", i);
			return -1;
		}
	}
	qsort(tags, tag_count, sizeof(*tags), compare_tags);
	for (size_t i = 0; i + 1 < tag_count; ++i) {
		if (strcmp(tags[i].key, tags[i + 1].key) == 0) {
			yanet_error_add(err, "duplicate key '%s'", tags[i].key);
			return -1;
		}
	}

	return 0;
}

int
cp_config_counter_storage_registry_insert(
	struct cp_config_counter_storage_registry *registry,
	const struct counter_tag *const_tags,
	size_t tag_count,
	struct counter_storage *storage,
	yanet_error **err
) {
	if (tag_count > MAX_TAG_COUNT) {
		yanet_error_add(err, "tag count exceeds max %d", MAX_TAG_COUNT);
		return -1;
	}
	struct counter_tag tags[MAX_TAG_COUNT];
	for (size_t i = 0; i < tag_count; ++i) {
		tags[i] = const_tags[i];
	}

	if (normalize_tags(tags, tag_count, false, err) != 0) {
		return -1;
	}

	if (tag_index_lookup(registry, tags, tag_count) != HASH_INDEX_INVALID) {
		yanet_error_add(err, "already exists");
		return -1;
	}

	if (registry->count == registry->capacity) {
		struct memory_context *mctx =
			ADDR_OF(&registry->memory_context);
		struct cp_counter_storage *items = memory_balloc(
			mctx, registry->capacity * 2 * sizeof(*items)
		);
		if (items == NULL) {
			yanet_error_add(err, "failed to allocate storage");
			return -1;
		}
		struct cp_counter_storage *prev_items =
			ADDR_OF(&registry->items);
		for (size_t i = 0; i < registry->count; ++i) {
			struct cp_counter_storage *dst = items + i;
			struct cp_counter_storage *src = prev_items + i;
			memcpy(dst->tags, src->tags, sizeof(dst->tags));
			dst->tag_count = src->tag_count;
			EQUATE_OFFSET(&dst->storage, &src->storage);
		}
		memory_bfree(
			mctx, prev_items, registry->count * sizeof(*items)
		);
		SET_OFFSET_OF(&registry->items, items);
		registry->capacity *= 2;
	}

	// The guard, not the doubling above, decides: a failed index growth
	// on an earlier insert can leave the index one doubling behind, and
	// a failed array growth can leave it one ahead.
	if (registry->tag_index.capacity < registry->capacity) {
		if (tag_index_grow(registry, registry->capacity)) {
			yanet_error_add(
				err, "failed to allocate counter storage index"
			);
			return -1;
		}
	}

	struct cp_counter_storage *dst =
		ADDR_OF(&registry->items) + registry->count;
	SET_OFFSET_OF(&dst->storage, storage);
	dst->tag_count = tag_count;
	for (size_t i = 0; i < tag_count; ++i) {
		dst->tags[i] = tags[i];
	}

	if (hash_index_insert(
		    &registry->tag_index,
		    tags_hash(tags, tag_count),
		    (uint32_t)registry->count
	    )) {
		yanet_error_add(
			err, "failed to allocate counter storage index"
		);
		return -1;
	}

	registry->count += 1;

	return 0;
}

static int
check_match(
	const struct counter_tag *filter,
	size_t filter_count,
	const struct counter_tag *present,
	size_t present_count
) {
	size_t j = 0;
	for (size_t i = 0; i < filter_count; ++i) {
		while (j < present_count &&
		       strcmp(filter[i].key, present[j].key) > 0) {
			++j;
		}
		bool key_present = j < present_count &&
				   strcmp(filter[i].key, present[j].key) == 0;
		bool ok;
		if (strcmp(filter[i].value, "") == 0) {
			ok = !key_present;
		} else if (strcmp(filter[i].value, "*") == 0) {
			ok = key_present;
		} else {
			ok = key_present &&
			     strcmp(filter[i].value, present[j].value) == 0;
		}
		if (!ok) {
			return 0;
		}
	}
	return 1;
}

struct cp_counter_storage **
cp_config_counter_storage_registry_find(
	struct cp_config_counter_storage_registry *registry,
	const struct counter_tag *const_tags,
	size_t tag_count,
	yanet_error **err
) {
	if (tag_count > MAX_TAG_COUNT) {
		yanet_error_add(err, "tag count exceeds max %d", MAX_TAG_COUNT);
		return NULL;
	}
	struct counter_tag tags[MAX_TAG_COUNT];
	for (size_t i = 0; i < tag_count; ++i) {
		tags[i] = const_tags[i];
	}

	if (normalize_tags(tags, tag_count, true, err) != 0) {
		return NULL;
	}

	size_t cnt = 0;
	struct cp_counter_storage *items = ADDR_OF(&registry->items);
	for (size_t i = 0; i < registry->count; ++i) {
		if (check_match(
			    tags, tag_count, items[i].tags, items[i].tag_count
		    ) == 1) {
			++cnt;
		}
	}

	struct cp_counter_storage **list = malloc((cnt + 1) * sizeof(*list));
	if (list == NULL) {
		yanet_error_add(err, "malloc failed");
		return NULL;
	}

	cnt = 0;
	for (size_t i = 0; i < registry->count; ++i) {
		if (check_match(
			    tags, tag_count, items[i].tags, items[i].tag_count
		    ) == 1) {
			list[cnt++] = items + i;
		}
	}

	list[cnt] = NULL;

	return list;
}

struct counter_storage *
cp_config_counter_storage_registry_lookup_exact(
	struct cp_config_counter_storage_registry *registry,
	const struct counter_tag *const_tags,
	size_t tag_count
) {
	if (tag_count > MAX_TAG_COUNT) {
		return NULL;
	}
	struct counter_tag tags[MAX_TAG_COUNT];
	for (size_t i = 0; i < tag_count; ++i) {
		tags[i] = const_tags[i];
	}

	if (normalize_tags(tags, tag_count, true, NULL) != 0) {
		return NULL;
	}

	uint32_t item_idx = tag_index_lookup(registry, tags, tag_count);
	if (item_idx == HASH_INDEX_INVALID) {
		return NULL;
	}
	struct cp_counter_storage *item = ADDR_OF(&registry->items) + item_idx;
	return ADDR_OF(&item->storage);
}

void
cp_config_counter_storage_registry_fini(
	struct cp_config_counter_storage_registry *registry
) {
	struct memory_context *mctx = ADDR_OF(&registry->memory_context);
	if (mctx == NULL) {
		return;
	}
	hash_index_fini(&registry->tag_index);
	struct cp_counter_storage *items = ADDR_OF(&registry->items);
	memory_bfree(mctx, items, sizeof(*items) * registry->capacity);
	memset(registry, 0, sizeof(*registry));
}

struct counter_storage *
cp_config_counter_storage_registry_lookup_device(
	struct cp_config_counter_storage_registry *registry,
	const char *device_name
) {
	struct counter_tag tags[] = {
		counter_tag_init("device", device_name),
		counter_tag_init("kind", "device")
	};
	return cp_config_counter_storage_registry_lookup_exact(
		registry, tags, 2
	);
}

int
cp_config_counter_storage_registry_insert_device(
	struct cp_config_counter_storage_registry *registry,
	const char *device_name,
	struct counter_storage *counter_storage,
	yanet_error **err
) {
	struct counter_tag tags[] = {
		counter_tag_init("device", device_name),
		counter_tag_init("kind", "device"),
	};
	return cp_config_counter_storage_registry_insert(
		registry, tags, 2, counter_storage, err
	);
}

struct counter_storage *
cp_config_counter_storage_registry_lookup_pipeline(
	struct cp_config_counter_storage_registry *registry,
	const char *device_name,
	const char *pipeline_name
) {
	struct counter_tag tags[] = {
		counter_tag_init("device", device_name),
		counter_tag_init("pipeline", pipeline_name),
		counter_tag_init("kind", "pipeline")
	};
	return cp_config_counter_storage_registry_lookup_exact(
		registry, tags, 3
	);
}

int
cp_config_counter_storage_registry_insert_pipeline(
	struct cp_config_counter_storage_registry *registry,
	const char *device_name,
	const char *pipeline_name,
	struct counter_storage *counter_storage,
	yanet_error **err
) {
	struct counter_tag tags[] = {
		counter_tag_init("device", device_name),
		counter_tag_init("pipeline", pipeline_name),
		counter_tag_init("kind", "pipeline"),
	};
	return cp_config_counter_storage_registry_insert(
		registry, tags, 3, counter_storage, err
	);
}

struct counter_storage *
cp_config_counter_storage_registry_lookup_function(
	struct cp_config_counter_storage_registry *registry,
	const char *device_name,
	const char *pipeline_name,
	const char *function_name
) {
	struct counter_tag tags[] = {
		counter_tag_init("device", device_name),
		counter_tag_init("pipeline", pipeline_name),
		counter_tag_init("function", function_name),
		counter_tag_init("kind", "function")
	};
	return cp_config_counter_storage_registry_lookup_exact(
		registry, tags, 4
	);
}

int
cp_config_counter_storage_registry_insert_function(
	struct cp_config_counter_storage_registry *registry,
	const char *device_name,
	const char *pipeline_name,
	const char *function_name,
	struct counter_storage *counter_storage,
	yanet_error **err
) {
	struct counter_tag tags[] = {
		counter_tag_init("device", device_name),
		counter_tag_init("pipeline", pipeline_name),
		counter_tag_init("function", function_name),
		counter_tag_init("kind", "function"),
	};
	return cp_config_counter_storage_registry_insert(
		registry, tags, 4, counter_storage, err
	);
}

struct counter_storage *
cp_config_counter_storage_registry_lookup_chain(
	struct cp_config_counter_storage_registry *registry,
	const char *device_name,
	const char *pipeline_name,
	const char *function_name,
	const char *chain_name
) {
	struct counter_tag tags[] = {
		counter_tag_init("device", device_name),
		counter_tag_init("pipeline", pipeline_name),
		counter_tag_init("function", function_name),
		counter_tag_init("chain", chain_name),
		counter_tag_init("kind", "chain")
	};
	return cp_config_counter_storage_registry_lookup_exact(
		registry, tags, 5
	);
}

int
cp_config_counter_storage_registry_insert_chain(
	struct cp_config_counter_storage_registry *registry,
	const char *device_name,
	const char *pipeline_name,
	const char *function_name,
	const char *chain_name,
	struct counter_storage *counter_storage,
	yanet_error **err
) {
	struct counter_tag tags[] = {
		counter_tag_init("device", device_name),
		counter_tag_init("pipeline", pipeline_name),
		counter_tag_init("function", function_name),
		counter_tag_init("chain", chain_name),
		counter_tag_init("kind", "chain"),
	};
	return cp_config_counter_storage_registry_insert(
		registry, tags, 5, counter_storage, err
	);
}

struct counter_storage *
cp_config_counter_storage_registry_lookup_module(
	struct cp_config_counter_storage_registry *registry,
	const char *device_name,
	const char *pipeline_name,
	const char *function_name,
	const char *chain_name,
	const char *module_type,
	const char *module_name
) {
	struct counter_tag tags[] = {
		counter_tag_init("device", device_name),
		counter_tag_init("pipeline", pipeline_name),
		counter_tag_init("function", function_name),
		counter_tag_init("chain", chain_name),
		counter_tag_init("module_type", module_type),
		counter_tag_init("module_name", module_name),
		counter_tag_init("kind", "module"),
	};
	return cp_config_counter_storage_registry_lookup_exact(
		registry, tags, 7
	);
}

int
cp_config_counter_storage_registry_insert_module(
	struct cp_config_counter_storage_registry *registry,
	const char *device_name,
	const char *pipeline_name,
	const char *function_name,
	const char *chain_name,
	const char *module_type,
	const char *module_name,
	struct counter_storage *counter_storage,
	yanet_error **err
) {
	struct counter_tag tags[] = {
		counter_tag_init("device", device_name),
		counter_tag_init("pipeline", pipeline_name),
		counter_tag_init("function", function_name),
		counter_tag_init("chain", chain_name),
		counter_tag_init("module_type", module_type),
		counter_tag_init("module_name", module_name),
		counter_tag_init("kind", "module"),
	};
	return cp_config_counter_storage_registry_insert(
		registry, tags, 7, counter_storage, err
	);
}

struct counter_storage *
cp_config_counter_storage_registry_lookup_module_tagged(
	struct cp_config_counter_storage_registry *registry,
	const char *device_name,
	const char *pipeline_name,
	const char *function_name,
	const char *chain_name,
	const char *module_type,
	const char *module_name,
	const char *registry_tag
) {
	struct counter_tag tags[] = {
		counter_tag_init("device", device_name),
		counter_tag_init("pipeline", pipeline_name),
		counter_tag_init("function", function_name),
		counter_tag_init("chain", chain_name),
		counter_tag_init("module_type", module_type),
		counter_tag_init("module_name", module_name),
		counter_tag_init("kind", "runtime"),
		counter_tag_init("config", registry_tag),
	};
	return cp_config_counter_storage_registry_lookup_exact(
		registry, tags, 8
	);
}

int
cp_config_counter_storage_registry_insert_module_tagged(
	struct cp_config_counter_storage_registry *registry,
	const char *device_name,
	const char *pipeline_name,
	const char *function_name,
	const char *chain_name,
	const char *module_type,
	const char *module_name,
	const char *registry_tag,
	struct counter_storage *counter_storage,
	yanet_error **err
) {
	struct counter_tag tags[] = {
		counter_tag_init("device", device_name),
		counter_tag_init("pipeline", pipeline_name),
		counter_tag_init("function", function_name),
		counter_tag_init("chain", chain_name),
		counter_tag_init("module_type", module_type),
		counter_tag_init("module_name", module_name),
		counter_tag_init("kind", "runtime"),
		counter_tag_init("config", registry_tag),
	};
	return cp_config_counter_storage_registry_insert(
		registry, tags, 8, counter_storage, err
	);
}

struct counter_storage *
cp_config_counter_storage_registry_lookup_object(
	struct cp_config_counter_storage_registry *registry,
	const char *object_type,
	const char *object_name
) {
	struct counter_tag tags[] = {
		counter_tag_init("object_type", object_type),
		counter_tag_init("object_name", object_name),
		counter_tag_init("kind", "object")
	};
	return cp_config_counter_storage_registry_lookup_exact(
		registry, tags, 3
	);
}

int
cp_config_counter_storage_registry_insert_object(
	struct cp_config_counter_storage_registry *registry,
	const char *object_type,
	const char *object_name,
	struct counter_storage *counter_storage,
	yanet_error **err
) {
	struct counter_tag tags[] = {
		counter_tag_init("object_type", object_type),
		counter_tag_init("object_name", object_name),
		counter_tag_init("kind", "object"),
	};
	return cp_config_counter_storage_registry_insert(
		registry, tags, 3, counter_storage, err
	);
}

struct counter_storage *
cp_config_counter_storage_registry_lookup_module_object_link(
	struct cp_config_counter_storage_registry *registry,
	const char *device_name,
	const char *pipeline_name,
	const char *function_name,
	const char *chain_name,
	const char *module_type,
	const char *module_name,
	const char *object_type,
	const char *object_name
) {
	struct counter_tag tags[] = {
		counter_tag_init("device", device_name),
		counter_tag_init("pipeline", pipeline_name),
		counter_tag_init("function", function_name),
		counter_tag_init("chain", chain_name),
		counter_tag_init("module_type", module_type),
		counter_tag_init("module_name", module_name),
		counter_tag_init("object_type", object_type),
		counter_tag_init("object_name", object_name),
		counter_tag_init("kind", "module_object_link")
	};
	return cp_config_counter_storage_registry_lookup_exact(
		registry, tags, 9
	);
}

int
cp_config_counter_storage_registry_insert_module_object_link(
	struct cp_config_counter_storage_registry *registry,
	const char *device_name,
	const char *pipeline_name,
	const char *function_name,
	const char *chain_name,
	const char *module_type,
	const char *module_name,
	const char *object_type,
	const char *object_name,
	struct counter_storage *counter_storage,
	yanet_error **err
) {
	struct counter_tag tags[] = {
		counter_tag_init("device", device_name),
		counter_tag_init("pipeline", pipeline_name),
		counter_tag_init("function", function_name),
		counter_tag_init("chain", chain_name),
		counter_tag_init("module_type", module_type),
		counter_tag_init("module_name", module_name),
		counter_tag_init("object_type", object_type),
		counter_tag_init("object_name", object_name),
		counter_tag_init("kind", "module_object_link")
	};
	return cp_config_counter_storage_registry_insert(
		registry, tags, 9, counter_storage, err
	);
}
