#include "cp_counter.h"

#include "common/memory.h"

#include "common/memory_address.h"
#include "counters/counters.h"
#include "lib/errors/errors.h"
#include <stdlib.h>
#include <string.h>

#define COUNTER_REGISTRY_PREALLOC 8

static int
compare_tags(const void *left, const void *right) {
	const struct counter_tag *left_tag = (const struct counter_tag *)left;
	const struct counter_tag *right_tag = (const struct counter_tag *)right;
	return strcmp(left_tag->key, right_tag->key);
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

	SET_OFFSET_OF(&registry->items, items);
	registry->capacity = COUNTER_REGISTRY_PREALLOC;
	registry->count = 0;
	SET_OFFSET_OF(&registry->memory_context, memory_context);
	return 0;
}

static int
validate_tag(struct counter_tag *tag, bool predicate, yanet_error **err) {
	if (tag->key == NULL) {
		yanet_error_add(err, "key is required");
		return -1;
	}
	if (tag->value == NULL) {
		yanet_error_add(err, "value is required");
		return -1;
	}
	if (strnlen(tag->key, KEY_MAX_SIZE) == KEY_MAX_SIZE) {
		yanet_error_add(
			err, "key length exceeds max %d", KEY_MAX_SIZE - 1
		);
		return -1;
	}
	if (strnlen(tag->value, VALUE_MAX_SIZE) == VALUE_MAX_SIZE) {
		yanet_error_add(
			err, "value length exceeds max %d", VALUE_MAX_SIZE - 1
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

static struct cp_counter_storage *
find_existing(
	struct cp_config_counter_storage_registry *registry,
	struct counter_tag *tags,
	size_t tag_count
) {
	struct cp_counter_storage *items = ADDR_OF(&registry->items);
	for (size_t i = 0; i < registry->count; ++i) {
		struct cp_counter_storage *cur = items + i;
		if (cur->tag_count != tag_count) {
			continue;
		}
		int equals = 1;
		for (size_t j = 0; j < tag_count; ++j) {
			if (strcmp(tags[j].key, cur->tags[j].key) != 0 ||
			    strcmp(tags[j].value, cur->tags[j].value) != 0) {
				equals = 0;
				break;
			}
		}
		if (equals) {
			return cur;
		}
	}
	return NULL;
}

int
cp_config_counter_storage_registry_insert(
	struct cp_config_counter_storage_registry *registry,
	const struct counter_tag *const_tags,
	size_t tag_count,
	struct counter_storage *storage,
	uint64_t worker_idx,
	uint64_t worker_count,
	yanet_error **err
) {
	if (tag_count > MAX_TAG_COUNT) {
		yanet_error_add(err, "tag count exceeds max %d", MAX_TAG_COUNT);
		return -1;
	}
	if (worker_idx >= worker_count) {
		yanet_error_add(
			err,
			"worker index %lu out of range %lu",
			worker_idx,
			worker_count
		);
		return -1;
	}
	struct counter_tag tags[MAX_TAG_COUNT];
	for (size_t i = 0; i < tag_count; ++i) {
		tags[i].key = const_tags[i].key;
		tags[i].value = const_tags[i].value;
	}

	if (normalize_tags(tags, tag_count, false, err) != 0) {
		return -1;
	}

	struct memory_context *mctx = ADDR_OF(&registry->memory_context);

	// A registry entity is inserted once per worker: the first insert
	// creates the item and its per-worker storages array, later inserts
	// only fill their worker's slot.
	struct cp_counter_storage *item =
		find_existing(registry, tags, tag_count);
	if (item != NULL) {
		struct counter_storage **storages = ADDR_OF(&item->storages);
		SET_OFFSET_OF(storages + worker_idx, storage);
		return 0;
	}

	if (registry->count == registry->capacity) {
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
			dst->worker_count = src->worker_count;
			EQUATE_OFFSET(&dst->storages, &src->storages);
		}
		memory_bfree(
			mctx, prev_items, registry->count * sizeof(*items)
		);
		SET_OFFSET_OF(&registry->items, items);
		registry->capacity *= 2;
	}

	struct counter_storage **storages = memory_balloc(
		mctx, worker_count * sizeof(struct counter_storage *)
	);
	if (storages == NULL) {
		yanet_error_add(err, "failed to allocate per-worker storages");
		return -1;
	}
	memset(storages, 0, worker_count * sizeof(struct counter_storage *));

	struct cp_counter_storage *dst =
		ADDR_OF(&registry->items) + registry->count;
	dst->worker_count = worker_count;
	SET_OFFSET_OF(&dst->storages, storages);
	SET_OFFSET_OF(storages + worker_idx, storage);
	dst->tag_count = tag_count;
	for (size_t i = 0; i < tag_count; ++i) {
		struct cp_counter_tag *dst_tag = &dst->tags[i];
		struct counter_tag *src = &tags[i];
		strcpy(dst_tag->key, src->key);
		strcpy(dst_tag->value, src->value);
	}

	registry->count += 1;

	return 0;
}

static int
check_match(
	const struct counter_tag *filter,
	size_t filter_count,
	const struct cp_counter_tag *present,
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
		tags[i].key = const_tags[i].key;
		tags[i].value = const_tags[i].value;
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

void
cp_config_counter_storage_registry_fini(
	struct cp_config_counter_storage_registry *registry
) {
	struct memory_context *mctx = ADDR_OF(&registry->memory_context);
	if (mctx == NULL) {
		return;
	}
	struct cp_counter_storage *items = ADDR_OF(&registry->items);
	// The counter_storage objects are owned by the ectx; here only the
	// per-item per-worker pointer arrays are released.
	for (size_t i = 0; i < registry->count; ++i) {
		struct counter_storage **storages = ADDR_OF(&items[i].storages);
		if (storages != NULL) {
			memory_bfree(
				mctx,
				storages,
				items[i].worker_count *
					sizeof(struct counter_storage *)
			);
		}
	}
	memory_bfree(mctx, items, sizeof(*items) * registry->capacity);
	memset(registry, 0, sizeof(*registry));
}

static struct counter_storage *
get_one(struct cp_config_counter_storage_registry *registry,
	struct counter_tag *tags,
	size_t tag_count,
	uint64_t worker_idx) {
	struct cp_counter_storage **items =
		cp_config_counter_storage_registry_find(
			registry, tags, tag_count, NULL
		);
	if (items == NULL || items[0] == NULL) {
		free(items);
		return NULL;
	}
	struct counter_storage *result = NULL;
	if (worker_idx < items[0]->worker_count) {
		struct counter_storage **storages =
			ADDR_OF(&items[0]->storages);
		result = ADDR_OF(storages + worker_idx);
	}
	free(items);
	return result;
}

struct counter_storage *
cp_config_counter_storage_registry_lookup_device(
	struct cp_config_counter_storage_registry *registry,
	const char *device_name,
	uint64_t worker_idx
) {
	struct counter_tag tags[] = {
		{.key = "device", .value = device_name},
		{.key = "pipeline", .value = ""}
	};
	return get_one(registry, tags, 2, worker_idx);
}

int
cp_config_counter_storage_registry_insert_device(
	struct cp_config_counter_storage_registry *registry,
	const char *device_name,
	struct counter_storage *counter_storage,
	uint64_t worker_idx,
	uint64_t worker_count,
	yanet_error **err
) {
	struct counter_tag tag = {.key = "device", .value = device_name};
	return cp_config_counter_storage_registry_insert(
		registry,
		&tag,
		1,
		counter_storage,
		worker_idx,
		worker_count,
		err
	);
}

struct counter_storage *
cp_config_counter_storage_registry_lookup_pipeline(
	struct cp_config_counter_storage_registry *registry,
	const char *device_name,
	const char *pipeline_name,
	uint64_t worker_idx
) {
	struct counter_tag tags[] = {
		{.key = "device", .value = device_name},
		{.key = "pipeline", .value = pipeline_name},
		{
			.key = "function",
			.value = "",
		}
	};
	return get_one(registry, tags, 3, worker_idx);
}

int
cp_config_counter_storage_registry_insert_pipeline(
	struct cp_config_counter_storage_registry *registry,
	const char *device_name,
	const char *pipeline_name,
	struct counter_storage *counter_storage,
	uint64_t worker_idx,
	uint64_t worker_count,
	yanet_error **err
) {
	struct counter_tag tags[] = {
		{.key = "device", .value = device_name},
		{.key = "pipeline", .value = pipeline_name}
	};
	return cp_config_counter_storage_registry_insert(
		registry,
		tags,
		2,
		counter_storage,
		worker_idx,
		worker_count,
		err
	);
}

struct counter_storage *
cp_config_counter_storage_registry_lookup_function(
	struct cp_config_counter_storage_registry *registry,
	const char *device_name,
	const char *pipeline_name,
	const char *function_name,
	uint64_t worker_idx
) {
	struct counter_tag tags[] = {
		{.key = "device", .value = device_name},
		{.key = "pipeline", .value = pipeline_name},
		{
			.key = "function",
			.value = function_name,
		},
		{.key = "chain", .value = ""}
	};
	return get_one(registry, tags, 4, worker_idx);
}

int
cp_config_counter_storage_registry_insert_function(
	struct cp_config_counter_storage_registry *registry,
	const char *device_name,
	const char *pipeline_name,
	const char *function_name,
	struct counter_storage *counter_storage,
	uint64_t worker_idx,
	uint64_t worker_count,
	yanet_error **err
) {
	struct counter_tag tags[] = {
		{.key = "device", .value = device_name},
		{.key = "pipeline", .value = pipeline_name},
		{.key = "function", .value = function_name}
	};
	return cp_config_counter_storage_registry_insert(
		registry,
		tags,
		3,
		counter_storage,
		worker_idx,
		worker_count,
		err
	);
}

struct counter_storage *
cp_config_counter_storage_registry_lookup_chain(
	struct cp_config_counter_storage_registry *registry,
	const char *device_name,
	const char *pipeline_name,
	const char *function_name,
	const char *chain_name,
	uint64_t worker_idx
) {
	struct counter_tag tags[] = {
		{.key = "device", .value = device_name},
		{.key = "pipeline", .value = pipeline_name},
		{
			.key = "function",
			.value = function_name,
		},
		{.key = "chain", .value = chain_name},
		{.key = "module_type", .value = ""}
	};
	return get_one(registry, tags, 5, worker_idx);
}

int
cp_config_counter_storage_registry_insert_chain(
	struct cp_config_counter_storage_registry *registry,
	const char *device_name,
	const char *pipeline_name,
	const char *function_name,
	const char *chain_name,
	struct counter_storage *counter_storage,
	uint64_t worker_idx,
	uint64_t worker_count,
	yanet_error **err
) {
	struct counter_tag tags[] = {
		{.key = "device", .value = device_name},
		{.key = "pipeline", .value = pipeline_name},
		{.key = "function", .value = function_name},
		{.key = "chain", .value = chain_name}
	};
	return cp_config_counter_storage_registry_insert(
		registry,
		tags,
		4,
		counter_storage,
		worker_idx,
		worker_count,
		err
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
	const char *module_name,
	uint64_t worker_idx
) {
	struct counter_tag tags[] = {
		{.key = "device", .value = device_name},
		{.key = "pipeline", .value = pipeline_name},
		{
			.key = "function",
			.value = function_name,
		},
		{.key = "chain", .value = chain_name},
		{.key = "module_type", .value = module_type},
		{.key = "module_name", .value = module_name}
	};
	return get_one(registry, tags, 6, worker_idx);
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
	uint64_t worker_idx,
	uint64_t worker_count,
	yanet_error **err
) {
	struct counter_tag tags[] = {
		{.key = "device", .value = device_name},
		{.key = "pipeline", .value = pipeline_name},
		{.key = "function", .value = function_name},
		{.key = "chain", .value = chain_name},
		{.key = "module_type", .value = module_type},
		{.key = "module_name", .value = module_name}
	};
	return cp_config_counter_storage_registry_insert(
		registry,
		tags,
		6,
		counter_storage,
		worker_idx,
		worker_count,
		err
	);
}
