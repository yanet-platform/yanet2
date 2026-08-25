#include "agent.h"

#include <stdbool.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "common/hash.h"
#include "common/memory_address.h"

#include "lib/controlplane/config/cp_module.h"
#include "lib/controlplane/config/zone.h"
#include "lib/counters/counter_pattern.h"
#include "lib/counters/counters.h"
#include "lib/dataplane/config/zone.h"
#include "lib/dataplane/pipeline/econtext.h"

#include "lib/errors/errors.h"

#include "api/agent.h"
#include "api/counter.h"

struct counter_query {
	struct counter_pattern_set patterns;
};

enum yanet_counter_query_result
yanet_counter_query_compile(
	const char *const *patterns,
	size_t count,
	struct counter_query **out,
	yanet_error **err
) {
	*out = NULL;

	struct counter_query *query = calloc(1, sizeof(*query));
	if (query == NULL) {
		yanet_error_add(err, "failed to allocate a counter query");
		return YANET_COUNTER_QUERY_NOMEM;
	}

	char reason[256] = {0};
	enum counter_pattern_result res = counter_pattern_set_compile(
		&query->patterns, patterns, count, reason, sizeof(reason)
	);
	if (res != COUNTER_PATTERN_OK) {
		yanet_error_add(err, "%s", reason);
		free(query);
		return res == COUNTER_PATTERN_NOMEM
			       ? YANET_COUNTER_QUERY_NOMEM
			       : YANET_COUNTER_QUERY_REJECTED;
	}

	*out = query;

	return YANET_COUNTER_QUERY_OK;
}

void
yanet_counter_query_free(struct counter_query *query) {
	if (query == NULL) {
		return;
	}

	counter_pattern_set_free(&query->patterns);
	free(query);
}

struct counter_handle_list *
yanet_get_module_counters(
	struct dp_config *dp_config,
	const char *device_name,
	const char *pipeline_name,
	const char *function_name,
	const char *chain_name,
	const char *module_type,
	const char *module_name,
	const struct counter_query *query
) {
	// Select module-kind storages only; the module's runtime-kind
	// storages hold its per-rule counters and are read through
	// yanet_get_counters_by_tags, as are relation counters on
	// module_object_link-kind storages sharing these six tags.
	struct counter_tag tags[] = {
		{.key = "device", .value = device_name},
		{.key = "pipeline", .value = pipeline_name},
		{.key = "function", .value = function_name},
		{.key = "chain", .value = chain_name},
		{.key = "module_type", .value = module_type},
		{.key = "module_name", .value = module_name},
		{.key = "kind", .value = "module"},
	};
	return yanet_get_counters_by_tags(dp_config, tags, 7, query, NULL);
}

static size_t
counter_registry_match_count(
	struct counter_registry *registry,
	const struct counter_pattern_set *names
) {
	struct counter *counters = ADDR_OF(&registry->names);
	size_t matches = 0;
	for (uint64_t i = 0; i < registry->count; ++i) {
		if (counter_pattern_set_match(names, counters[i].name)) {
			++matches;
		}
	}
	return matches;
}

// Deep-copy a tag array into freshly allocated memory.
static struct counter_tag *
counter_tags_dup(const struct counter_tag *tags, size_t tag_count) {
	struct counter_tag *dup = malloc(tag_count * sizeof(*dup));
	if (dup == NULL) {
		return NULL;
	}
	for (size_t i = 0; i < tag_count; ++i) {
		dup[i].key = NULL;
		dup[i].value = NULL;
	}
	for (size_t i = 0; i < tag_count; ++i) {
		dup[i].key = strdup(tags[i].key);
		dup[i].value = strdup(tags[i].value);
		if (dup[i].key == NULL || dup[i].value == NULL) {
			for (size_t j = 0; j < tag_count; ++j) {
				free((void *)dup[j].key);
				free((void *)dup[j].value);
			}
			free(dup);
			return NULL;
		}
	}
	return dup;
}

static struct counter_tag *
cp_counter_storage_copy_tags(const struct cp_counter_storage *storage) {
	struct counter_tag view[MAX_TAG_COUNT];
	for (size_t i = 0; i < storage->tag_count; ++i) {
		view[i].key = storage->tags[i].key;
		view[i].value = storage->tags[i].value;
	}
	return counter_tags_dup(view, storage->tag_count);
}

// Copy one already-described counter's per-worker value snapshot into
// newly allocated memory, stashed behind the handle's opaque value array.
//
// The pointer array and every worker's value block live in one allocation:
// the pointer array first, then each worker's values back to back.
//
// The caller guarantees the per-worker storages stay alive for the
// duration of this call.
static int
counter_handle_copy_values(
	struct counter_handle *dst,
	struct counter_storage **worker_storages,
	uint64_t worker_count,
	uint64_t idx
) {
	size_t ptr_array_size = worker_count * sizeof(uint64_t *);

	uint8_t *base =
		malloc(ptr_array_size +
		       worker_count * dst->size * sizeof(uint64_t));
	if (base == NULL) {
		return -1;
	}

	uint64_t **values = (uint64_t **)base;
	uint64_t *value_blocks = (uint64_t *)(base + ptr_array_size);
	for (uint64_t w_idx = 0; w_idx < worker_count; ++w_idx) {
		if (dst->size == 0) {
			values[w_idx] = NULL;
			continue;
		}
		values[w_idx] = value_blocks + w_idx * dst->size;
		struct counter_value_handle *handle =
			counter_get_value_handle(idx, worker_storages[w_idx]);
		memcpy(values[w_idx],
		       counter_handle_get_value(handle),
		       dst->size * sizeof(uint64_t));
	}
	dst->values = values;
	return 0;
}

// Snapshot a counter's per-worker values into heap memory and stash the
// result behind the opaque counter_handle.values.
//
// Each worker has its own single-instance storage. The caller gathers one
// storage per worker into worker_storages (a plain C-pointer array). These
// storages are dataplane-owned and outlive every configuration generation,
// so no lock or generation pin is required around this call.
static int
fill_counter_handle(
	struct counter_handle *dst,
	struct counter_storage **worker_storages,
	uint64_t worker_count,
	uint64_t idx,
	struct counter_tag *tags,
	size_t tag_count
) {
	struct counter_storage *storage0 = worker_storages[0];
	struct counter_registry *reg = ADDR_OF(&storage0->registry);
	struct counter *counters = ADDR_OF(&reg->names);
	strtcpy(dst->name, counters[idx].name, sizeof(dst->name));
	dst->size = counters[idx].size;
	dst->gen = counters[idx].gen;

	// Attach the tags before the fallible allocation so the list free path
	// reclaims them even when this handle's value array is left NULL.
	dst->tags = tags;
	dst->tag_count = tag_count;

	return counter_handle_copy_values(
		dst, worker_storages, worker_count, idx
	);
}

// Per-worker counter storages matching one tag predicate.
//
// Each worker's entry is a NULL-terminated array of matches, or NULL for
// a worker without an execution context. The registries of two workers
// are built independently, so their match arrays may differ in length
// and content.
struct worker_counter_matches {
	struct cp_counter_storage ***by_worker;
	uint64_t worker_count;
};

static void
worker_counter_matches_free(struct worker_counter_matches *matches) {
	if (matches->by_worker == NULL) {
		return;
	}
	for (uint64_t w_idx = 0; w_idx < matches->worker_count; ++w_idx) {
		free(matches->by_worker[w_idx]);
	}
	free(matches->by_worker);
	matches->by_worker = NULL;
}

// Collect the per-worker counter storages matching a tag predicate.
//
// The generation MUST stay pinned by the caller for the whole call, since
// this only follows registries reachable through it.
static int
worker_counter_matches_collect(
	struct cp_config_gen *config_gen,
	uint64_t worker_count,
	const struct counter_tag *tags,
	size_t tag_count,
	struct worker_counter_matches *out,
	yanet_error **err
) {
	out->worker_count = worker_count;
	out->by_worker = calloc(worker_count, sizeof(*out->by_worker));
	if (out->by_worker == NULL) {
		yanet_error_add(err, "malloc failed");
		return -1;
	}

	for (uint64_t w_idx = 0; w_idx < worker_count; ++w_idx) {
		struct config_gen_ectx *ectx =
			cp_config_gen_worker_ectx(config_gen, w_idx);
		if (ectx == NULL) {
			out->by_worker[w_idx] = NULL;
			continue;
		}
		out->by_worker[w_idx] = cp_config_counter_storage_registry_find(
			ADDR_OF(&ectx->counter_storage_registry),
			tags,
			tag_count,
			err
		);
		if (out->by_worker[w_idx] == NULL) {
			return -1;
		}
	}
	return 0;
}

// Allocate a handle list sized for match_count handles and zero it.
// Sets instance_count and count on success. The caller still owns filling
// in each handle.
static struct counter_handle_list *
counter_handle_list_alloc(
	uint64_t instance_count, size_t match_count, yanet_error **err
) {
	size_t list_size = sizeof(struct counter_handle_list) +
			   sizeof(struct counter_handle) * match_count;
	struct counter_handle_list *list =
		(struct counter_handle_list *)malloc(list_size);
	if (list == NULL) {
		yanet_error_add(err, "malloc failed");
		return NULL;
	}
	memset(list, 0, list_size);
	list->instance_count = instance_count;
	list->count = match_count;
	return list;
}

// Build one worker's handle list from its matching storages.
//
// matches is that worker's NULL-terminated storage match array, or NULL
// for a worker without an execution context. The resulting list carries
// instance_count == 1 and values snapshotted from that worker's own
// storages only. Handles of one storage share a tags copy and sit next
// to each other, matching the list free path's sharing convention.
static struct counter_handle_list *
worker_counter_list_build(
	struct cp_counter_storage **matches,
	const struct counter_pattern_set *names,
	yanet_error **err
) {
	if (matches == NULL) {
		return counter_handle_list_alloc(1, 0, err);
	}

	size_t match_count = 0;
	for (size_t i = 0; matches[i] != NULL; ++i) {
		struct counter_storage *storage = ADDR_OF(&matches[i]->storage);
		match_count += counter_registry_match_count(
			ADDR_OF(&storage->registry), names
		);
	}

	struct counter_handle_list *list =
		counter_handle_list_alloc(1, match_count, err);
	if (list == NULL) {
		return NULL;
	}

	size_t next = 0;
	for (size_t i = 0; matches[i] != NULL; ++i) {
		struct cp_counter_storage *cp_storage = matches[i];
		struct counter_storage *storage = ADDR_OF(&cp_storage->storage);
		struct counter_registry *registry = ADDR_OF(&storage->registry);
		struct counter *counters = ADDR_OF(&registry->names);
		struct counter_tag *storage_tags = NULL;
		for (uint64_t idx = 0; idx < registry->count; ++idx) {
			if (!counter_pattern_set_match(
				    names, counters[idx].name
			    )) {
				continue;
			}
			if (storage_tags == NULL) {
				storage_tags =
					cp_counter_storage_copy_tags(cp_storage
					);
				if (storage_tags == NULL) {
					yanet_error_add(err, "malloc failed");
					yanet_counter_handle_list_free(list);
					return NULL;
				}
			}
			struct counter_handle *dst = &list->counters[next];
			const struct counter *src = &counters[idx];
			strtcpy(dst->name, src->name, sizeof(dst->name));
			dst->size = src->size;
			dst->gen = src->gen;
			// Attach the tags before the fallible value copy so
			// the list free path reclaims them even when this
			// handle's value array is left NULL.
			dst->tags = storage_tags;
			dst->tag_count = cp_storage->tag_count;
			struct counter_storage *worker_storages[1] = {
				storage,
			};
			if (counter_handle_copy_values(
				    dst, worker_storages, 1, idx
			    )) {
				yanet_error_add(err, "malloc failed");
				yanet_counter_handle_list_free(list);
				return NULL;
			}
			++next;
		}
	}
	return list;
}

// FNV-1a over a tag's key and value, separated so that no key/value
// split of the same bytes hashes the same.
static uint64_t
counter_tag_hash(const struct counter_tag *tag) {
	uint64_t hash = 14695981039346656037ull;
	for (const char *str = tag->key; *str != '\0'; ++str) {
		hash = (hash ^ (uint64_t)(unsigned char)*str) *
		       1099511628211ull;
	}
	hash = (hash ^ 0x1f) * 1099511628211ull;
	for (const char *str = tag->value; *str != '\0'; ++str) {
		hash = (hash ^ (uint64_t)(unsigned char)*str) *
		       1099511628211ull;
	}
	return hash;
}

// Tag-set hash, commutative over the tags so that two storages carrying
// the same tags in different orders hash the same.
static uint64_t
counter_tag_set_hash(const struct counter_tag *tags, size_t tag_count) {
	uint64_t hash = 0;
	for (size_t idx = 0; idx < tag_count; ++idx) {
		hash ^= counter_tag_hash(tags + idx);
	}
	return hash;
}

static bool
counter_tag_set_equal(
	const struct counter_tag *left,
	size_t left_count,
	const struct counter_tag *right,
	size_t right_count
) {
	if (left_count != right_count) {
		return false;
	}
	for (size_t l = 0; l < left_count; ++l) {
		bool found = false;
		for (size_t r = 0; r < right_count; ++r) {
			if (strcmp(left[l].key, right[r].key) == 0 &&
			    strcmp(left[l].value, right[r].value) == 0) {
				found = true;
				break;
			}
		}
		if (!found) {
			return false;
		}
	}
	return true;
}

// A distinct tag set seen during a merge, in first-seen order.
//
// tags are borrowed from the worker list that first carried the group
// and are only read for the duration of the merge.
struct counter_merge_group {
	const struct counter_tag *tags;
	size_t tag_count;
};

struct counter_merge_group_slot {
	uint64_t hash;
	// Group index plus one; zero marks an empty slot.
	uint64_t group;
};

struct counter_merge_counter_slot {
	uint64_t hash;
	// Merged handle index plus one; zero marks an empty slot.
	uint64_t handle;
};

// State of one merge of per-worker sets into a union list.
struct counter_merge {
	struct counter_handle_list *list;
	// Group of every merged handle, parallel to list->counters.
	uint64_t *handle_group;
	size_t used;

	struct counter_merge_group *groups;
	size_t group_count;

	struct counter_merge_group_slot *group_slots;
	size_t group_mask;

	struct counter_merge_counter_slot *counter_slots;
	size_t counter_mask;
};

// Smallest power of two not smaller than count.
static size_t
pow2_at_least(size_t count) {
	size_t size = 1;
	while (size < count) {
		size <<= 1;
	}
	return size;
}

// Release the merge indexes. The merged list stays owned by the caller.
static void
counter_merge_fini(struct counter_merge *merge) {
	free(merge->handle_group);
	free(merge->groups);
	free(merge->group_slots);
	free(merge->counter_slots);
}

// Allocate a merged list sized for total handles plus its indexes.
//
// On failure every allocation of this merge is already released.
static int
counter_merge_init(
	struct counter_merge *merge,
	uint64_t worker_count,
	size_t total,
	yanet_error **err
) {
	memset(merge, 0, sizeof(*merge));

	merge->list = counter_handle_list_alloc(worker_count, total, err);
	if (merge->list == NULL) {
		return -1;
	}
	merge->list->count = 0;

	if (total == 0) {
		return 0;
	}

	merge->handle_group = malloc(total * sizeof(*merge->handle_group));
	merge->groups = malloc(total * sizeof(*merge->groups));
	size_t slot_count = pow2_at_least(2 * total);
	merge->group_slots = calloc(slot_count, sizeof(*merge->group_slots));
	merge->counter_slots =
		calloc(slot_count, sizeof(*merge->counter_slots));
	if (merge->handle_group == NULL || merge->groups == NULL ||
	    merge->group_slots == NULL || merge->counter_slots == NULL) {
		yanet_error_add(err, "malloc failed");
		yanet_counter_handle_list_free(merge->list);
		merge->list = NULL;
		counter_merge_fini(merge);
		return -1;
	}
	merge->group_mask = slot_count - 1;
	merge->counter_mask = slot_count - 1;
	return 0;
}

// Return the group index of a handle's tag set, registering a new group
// when the set is seen for the first time.
static uint64_t
counter_merge_group(
	struct counter_merge *merge, const struct counter_handle *handle
) {
	uint64_t hash = counter_tag_set_hash(handle->tags, handle->tag_count);
	size_t slot = (size_t)hash & merge->group_mask;
	for (;;) {
		struct counter_merge_group_slot *cur =
			&merge->group_slots[slot];
		if (cur->group == 0) {
			cur->hash = hash;
			cur->group = merge->group_count + 1;
			merge->groups[merge->group_count] =
				(struct counter_merge_group){
					.tags = handle->tags,
					.tag_count = handle->tag_count,
				};
			return merge->group_count++;
		}
		if (cur->hash == hash) {
			uint64_t group = cur->group - 1;
			const struct counter_merge_group *known =
				&merge->groups[group];
			if (counter_tag_set_equal(
				    handle->tags,
				    handle->tag_count,
				    known->tags,
				    known->tag_count
			    )) {
				return group;
			}
		}
		slot = (slot + 1) & merge->group_mask;
	}
}

// Hash a merged counter's identity: its group and its name.
static uint64_t
counter_merge_counter_hash(uint64_t group, const char *name) {
	uint64_t hash = 14695981039346656037ull;
	for (; *name != '\0'; ++name) {
		hash = (hash ^ (uint64_t)(unsigned char)*name) *
		       1099511628211ull;
	}
	return wyhash64(hash ^ (group + 1));
}

// Return the merged handle index for (group, name), reserving the next
// free handle and setting *created when the counter is new to the merge.
static uint64_t
counter_merge_handle(
	struct counter_merge *merge,
	uint64_t group,
	const char *name,
	bool *created
) {
	uint64_t hash = counter_merge_counter_hash(group, name);
	size_t slot = (size_t)hash & merge->counter_mask;
	for (;;) {
		struct counter_merge_counter_slot *cur =
			&merge->counter_slots[slot];
		if (cur->handle == 0) {
			cur->hash = hash;
			cur->handle = merge->used + 1;
			merge->handle_group[merge->used] = group;
			*created = true;
			return merge->used++;
		}
		if (cur->hash == hash) {
			uint64_t handle = cur->handle - 1;
			if (merge->handle_group[handle] == group &&
			    strcmp(merge->list->counters[handle].name, name) ==
				    0) {
				*created = false;
				return handle;
			}
		}
		slot = (slot + 1) & merge->counter_mask;
	}
}

// Fill a freshly reserved merged handle from the first worker carrying
// the counter: metadata, its own tags copy, and a zeroed value block
// spanning every worker.
static int
counter_merge_handle_init(
	struct counter_merge *merge,
	uint64_t handle_idx,
	const struct counter_handle *src,
	yanet_error **err
) {
	struct counter_handle *dst = &merge->list->counters[handle_idx];
	strtcpy(dst->name, src->name, sizeof(dst->name));
	dst->size = src->size;
	dst->gen = src->gen;
	dst->tags = counter_tags_dup(src->tags, src->tag_count);
	if (dst->tags == NULL && src->tag_count > 0) {
		yanet_error_add(err, "malloc failed");
		return -1;
	}
	dst->tag_count = src->tag_count;

	uint64_t worker_count = merge->list->instance_count;
	size_t ptr_array_size = worker_count * sizeof(uint64_t *);
	uint8_t *base = calloc(
		1, ptr_array_size + worker_count * dst->size * sizeof(uint64_t)
	);
	if (base == NULL) {
		yanet_error_add(err, "malloc failed");
		return -1;
	}
	dst->values = (uint64_t **)base;
	uint64_t *blocks = (uint64_t *)(base + ptr_array_size);
	for (uint64_t w_idx = 0; w_idx < worker_count; ++w_idx) {
		if (dst->size == 0) {
			dst->values[w_idx] = NULL;
			continue;
		}
		dst->values[w_idx] = blocks + w_idx * dst->size;
	}
	return 0;
}

// Union of the per-worker matched counter sets.
//
// A counter's identity is its tag set plus its name. The first worker
// carrying it defines its position and metadata; every other worker
// that carries it has its snapshot copied into its instance slot, and
// the instance slots of workers without the counter stay zero.
static struct counter_handle_list *
counter_worker_sets_merge(
	const struct counter_worker_set_list *sets, yanet_error **err
) {
	uint64_t worker_count = sets->worker_count;

	size_t total = 0;
	for (uint64_t w_idx = 0; w_idx < worker_count; ++w_idx) {
		total += sets->sets[w_idx].counters->count;
	}

	struct counter_merge merge;
	if (counter_merge_init(&merge, worker_count, total, err)) {
		return NULL;
	}

	for (uint64_t w_idx = 0; w_idx < worker_count; ++w_idx) {
		struct counter_handle_list *src = sets->sets[w_idx].counters;
		for (size_t i = 0; i < src->count; ++i) {
			struct counter_handle *handle = &src->counters[i];
			uint64_t group = counter_merge_group(&merge, handle);
			bool created = false;
			uint64_t handle_idx = counter_merge_handle(
				&merge, group, handle->name, &created
			);
			if (created && counter_merge_handle_init(
					       &merge, handle_idx, handle, err
				       )) {
				goto error;
			}
			struct counter_handle *dst =
				&merge.list->counters[handle_idx];
			// Sizes come from the module's registry and are
			// expected to agree across workers; on disagreement
			// the first worker's size stays and this worker's
			// instance remains zero.
			if (handle->size != dst->size || dst->size == 0) {
				continue;
			}
			memcpy(dst->values[w_idx],
			       handle->values[0],
			       dst->size * sizeof(uint64_t));
		}
	}

	merge.list->count = merge.used;
	counter_merge_fini(&merge);
	return merge.list;

error:
	// Every handle below merge.used is initialized, including the one
	// whose init failed halfway, so publishing the count lets the free
	// path reclaim their tags and value blocks.
	merge.list->count = merge.used;
	yanet_counter_handle_list_free(merge.list);
	counter_merge_fini(&merge);
	return NULL;
}

// Build the per-worker set list from the collected matches.
static struct counter_worker_set_list *
counter_worker_set_list_build(
	const struct worker_counter_matches *matches,
	const struct counter_pattern_set *names,
	yanet_error **err
) {
	// Zero-initialized so the free path below only visits slots this
	// function actually filled in before a failure.
	struct counter_worker_set_list *sets =
		(struct counter_worker_set_list *)calloc(
			1,
			sizeof(*sets) +
				sizeof(sets->sets[0]) * matches->worker_count
		);
	if (sets == NULL) {
		yanet_error_add(err, "malloc failed");
		return NULL;
	}
	sets->worker_count = matches->worker_count;

	for (uint64_t w_idx = 0; w_idx < matches->worker_count; ++w_idx) {
		sets->sets[w_idx].worker_idx = w_idx;
		sets->sets[w_idx].counters = worker_counter_list_build(
			matches->by_worker[w_idx], names, err
		);
		if (sets->sets[w_idx].counters == NULL) {
			yanet_counter_worker_set_list_free(sets);
			return NULL;
		}
	}
	return sets;
}

// Match the tag and name predicates against every worker's own counter
// storage registry and return each worker's matched counters separately.
//
// The config lock is held only long enough to acquire the generation pin
// at the start and to drop it again at the end. The pin itself stays held
// for the whole call: matching, allocation and the value copy all run
// unlocked against the pinned generation, so a concurrent config update
// can retire and replace it mid-call without disturbing this read.
struct counter_worker_set_list *
yanet_get_counters_by_tags_per_worker(
	struct dp_config *dp_config,
	const struct counter_tag *tags,
	size_t tag_count,
	const struct counter_query *query,
	yanet_error **err
) {
	struct cp_config *cp_config = ADDR_OF(&dp_config->cp_config);

	struct counter_pattern_set any;
	counter_pattern_set_match_all(&any);
	const struct counter_pattern_set *names =
		query != NULL ? &query->patterns : &any;

	cp_config_lock(cp_config);
	uint64_t worker_count = dp_config->worker_count;
	struct cp_config_gen *config_gen = cp_config_gen_acquire(cp_config);
	cp_config_unlock(cp_config);

	struct worker_counter_matches matches = {
		.by_worker = NULL,
		.worker_count = 0,
	};
	struct counter_worker_set_list *sets = NULL;

	if (worker_counter_matches_collect(
		    config_gen, worker_count, tags, tag_count, &matches, err
	    )) {
		goto out;
	}

	sets = counter_worker_set_list_build(&matches, names, err);

out:
	worker_counter_matches_free(&matches);

	cp_config_lock(cp_config);
	cp_config_gen_release(cp_config, config_gen);
	cp_config_unlock(cp_config);

	return sets;
}

struct counter_worker_set *
yanet_get_counter_worker_set(
	struct counter_worker_set_list *sets, uint64_t worker_idx
) {
	if (sets == NULL || worker_idx >= sets->worker_count) {
		return NULL;
	}
	return sets->sets + worker_idx;
}

void
yanet_counter_worker_set_list_free(struct counter_worker_set_list *sets) {
	if (sets == NULL) {
		return;
	}
	for (uint64_t w_idx = 0; w_idx < sets->worker_count; ++w_idx) {
		yanet_counter_handle_list_free(sets->sets[w_idx].counters);
	}
	free(sets);
}

// Match the tag and name predicates against every worker's own registry
// and merge the sets into one list spanning all workers, with zero
// values wherever a worker does not carry the counter.
struct counter_handle_list *
yanet_get_counters_by_tags(
	struct dp_config *dp_config,
	const struct counter_tag *tags,
	size_t tag_count,
	const struct counter_query *query,
	yanet_error **err
) {
	struct counter_worker_set_list *sets =
		yanet_get_counters_by_tags_per_worker(
			dp_config, tags, tag_count, query, err
		);
	if (sets == NULL) {
		return NULL;
	}

	struct counter_handle_list *list = counter_worker_sets_merge(sets, err);
	yanet_counter_worker_set_list_free(sets);
	return list;
}

struct counter_handle_list *
yanet_get_chain_counters(
	struct dp_config *dp_config,
	const char *device_name,
	const char *pipeline_name,
	const char *function_name,
	const char *chain_name
) {
	struct counter_tag tags[] = {
		{.key = "device", .value = device_name},
		{.key = "pipeline", .value = pipeline_name},
		{.key = "function", .value = function_name},
		{.key = "chain", .value = chain_name},
		{.key = "kind", .value = "chain"}
	};
	return yanet_get_counters_by_tags(dp_config, tags, 5, NULL, NULL);
}

struct counter_handle_list *
yanet_get_function_counters(
	struct dp_config *dp_config,
	const char *device_name,
	const char *pipeline_name,
	const char *function_name
) {
	struct counter_tag tags[] = {
		{.key = "device", .value = device_name},
		{.key = "pipeline", .value = pipeline_name},
		{.key = "function", .value = function_name},
		{.key = "kind", .value = "function"}
	};
	return yanet_get_counters_by_tags(dp_config, tags, 4, NULL, NULL);
}

struct counter_handle_list *
yanet_get_pipeline_counters(
	struct dp_config *dp_config,
	const char *device_name,
	const char *pipeline_name
) {
	struct counter_tag tags[] = {
		{.key = "device", .value = device_name},
		{.key = "pipeline", .value = pipeline_name},
		{.key = "kind", .value = "pipeline"}
	};
	return yanet_get_counters_by_tags(dp_config, tags, 3, NULL, NULL);
}

struct counter_handle_list *
yanet_get_device_counters(
	struct dp_config *dp_config, const char *device_name
) {
	struct counter_tag tags[] = {
		{.key = "device", .value = device_name},
		{.key = "kind", .value = "device"}
	};
	return yanet_get_counters_by_tags(dp_config, tags, 2, NULL, NULL);
}

struct counter_handle_list *
yanet_get_object_counters(
	struct dp_config *dp_config,
	const char *object_type,
	const char *object_name
) {
	struct counter_tag tags[] = {
		{.key = "object_type", .value = object_type},
		{.key = "object_name", .value = object_name},
		{.key = "kind", .value = "object"}
	};
	return yanet_get_counters_by_tags(dp_config, tags, 3, NULL, NULL);
}

struct counter_handle *
yanet_get_counter(struct counter_handle_list *counters, uint64_t idx) {
	if (idx >= counters->count) {
		return NULL;
	}
	return counters->counters + idx;
}

uint64_t
yanet_get_counter_value(
	uint64_t **values, uint64_t value_idx, uint64_t worker_idx
) {
	return values[worker_idx][value_idx];
}

void
yanet_get_counter_values(
	uint64_t **values,
	uint64_t size,
	uint64_t instance_count,
	uint64_t *values_out
) {
	if (size == 0) {
		return;
	}
	for (uint64_t iidx = 0; iidx < instance_count; ++iidx) {
		memcpy(values_out + iidx * size,
		       values[iidx],
		       size * sizeof(uint64_t));
	}
}

static struct counter_handle_list *
counter_handle_list_build(
	struct counter_registry *counter_registry,
	struct counter_storage **storages,
	uint64_t worker_count
) {
	uint64_t count = counter_registry->count;

	struct counter_handle_list *list = (struct counter_handle_list *)malloc(
		sizeof(struct counter_handle_list) +
		sizeof(struct counter_handle) * count
	);

	if (list == NULL) {
		return NULL;
	}
	list->instance_count = worker_count;
	list->count = count;
	struct counter_handle *handlers = list->counters;

	for (uint64_t idx = 0; idx < count; ++idx) {
		handlers[idx].tag_count = 0;
		handlers[idx].tags = NULL;
		handlers[idx].values = NULL;
	}

	// storages holds one offset-pointer cell per worker; materialize a
	// plain storage pointer per worker before handing them to
	// fill_counter_handle, which indexes the array directly.
	struct counter_storage **worker_storages =
		malloc(worker_count * sizeof(*worker_storages));
	if (worker_storages == NULL) {
		yanet_counter_handle_list_free(list);
		return NULL;
	}
	for (uint64_t worker_idx = 0; worker_idx < worker_count; ++worker_idx) {
		worker_storages[worker_idx] = ADDR_OF(storages + worker_idx);
	}

	for (uint64_t idx = 0; idx < count; ++idx) {
		if (fill_counter_handle(
			    &handlers[idx],
			    worker_storages,
			    worker_count,
			    idx,
			    NULL,
			    0
		    )) {
			free(worker_storages);
			yanet_counter_handle_list_free(list);
			return NULL;
		}
	}

	free(worker_storages);
	return list;
}

struct counter_handle_list *
yanet_get_worker_counters(struct dp_config *dp_config) {
	return counter_handle_list_build(
		&dp_config->worker_counters,
		ADDR_OF(&dp_config->worker_counter_storages),
		dp_config->worker_counter_storage_count
	);
}

int
yanet_get_worker_counter_metadata(
	struct dp_config *dp_config,
	uint64_t worker_idx,
	struct worker_counter_metadata *metadata
) {
	if (dp_config == NULL || metadata == NULL) {
		return -1;
	}

	struct dp_worker **workers = ADDR_OF(&dp_config->workers);
	if (workers == NULL) {
		return -1;
	}

	if (worker_idx >= dp_config->worker_count) {
		return -1;
	}

	struct dp_worker *worker = ADDR_OF(&workers[worker_idx]);
	if (worker == NULL) {
		return -1;
	}

	metadata->core_id = worker->core_id;
	metadata->device_id = worker->device_id;
	metadata->queue_id = worker->queue_id;
	metadata->rx_burst_size = worker->rx_burst_size;

	return 0;
}

struct port_counter_group_list *
yanet_get_port_counters(struct dp_config *dp_config) {
	uint64_t port_count = dp_config->port_count;
	struct dp_port_counters *port_counters =
		ADDR_OF(&dp_config->port_counters);
	// The count and the array offset publish as separate stores, so a
	// reader can observe a nonzero count before the offset lands and
	// would otherwise index off a null base.
	if (port_count != 0 && port_counters == NULL) {
		return NULL;
	}

	struct port_counter_group_list *groups =
		(struct port_counter_group_list *)malloc(
			sizeof(struct port_counter_group_list) +
			sizeof(struct port_counter_group) * port_count
		);
	if (groups == NULL) {
		return NULL;
	}
	groups->port_count = port_count;

	for (uint64_t idx = 0; idx < port_count; ++idx) {
		struct dp_port_counters *pc = port_counters + idx;
		struct port_counter_group *group = groups->ports + idx;

		group->port_id = pc->port_id;
		strtcpy(group->port_name,
			pc->port_name,
			sizeof(group->port_name));
		group->counters = counter_handle_list_build(
			&pc->registry, &pc->storage, 1
		);
		if (group->counters == NULL) {
			groups->port_count = idx;
			yanet_port_counter_group_list_free(groups);
			return NULL;
		}
	}

	return groups;
}

struct port_counter_group *
yanet_get_port_counter_group(
	struct port_counter_group_list *groups, uint64_t idx
) {
	if (groups == NULL || idx >= groups->port_count) {
		return NULL;
	}
	return groups->ports + idx;
}

void
yanet_port_counter_group_list_free(struct port_counter_group_list *groups) {
	if (groups == NULL) {
		return;
	}
	for (uint64_t idx = 0; idx < groups->port_count; ++idx) {
		yanet_counter_handle_list_free(groups->ports[idx].counters);
	}
	free(groups);
}

void
yanet_counter_handle_list_free(struct counter_handle_list *counters) {
	if (counters == NULL) {
		return;
	}
	struct counter_handle *handles = counters->counters;
	for (size_t i = 0; i < counters->count; ++i) {
		if (i == 0 || handles[i].tags != handles[i - 1].tags) {
			for (size_t j = 0; j < handles[i].tag_count; ++j) {
				free((void *)handles[i].tags[j].key);
				free((void *)handles[i].tags[j].value);
			}
			free(handles[i].tags);
		}
		// This pointer is the base of the single allocation that also
		// holds every worker's value block, so freeing it here
		// reclaims all of it.
		free(handles[i].values);
	}
	free(counters);
}

int
yanet_module_performance_counters(
	struct module_performance_counters *counters,
	struct dp_config *dp_config,
	const char *device_name,
	const char *pipeline_name,
	const char *function_name,
	const char *chain_name,
	const char *module_type,
	const char *module_name,
	yanet_error **err
) {
	if (counters == NULL) {
		yanet_error_add(err, "counters parameter is NULL");
		goto err;
	}

	struct counter_handle_list *counter_list = yanet_get_module_counters(
		dp_config,
		device_name,
		pipeline_name,
		function_name,
		chain_name,
		module_type,
		module_name,
		NULL
	);

	if (counter_list == NULL) {
		yanet_error_add(
			err,
			"module counters not found for device='%s', "
			"pipeline='%s', function='%s', chain='%s', "
			"module_type='%s', module_name='%s'",
			device_name,
			pipeline_name,
			function_name,
			chain_name,
			module_type,
			module_name
		);
		goto err;
	}

	// Initialize tx/rx fields to 0
	counters->rx = 0;
	counters->rx_bytes = 0;
	counters->tx = 0;
	counters->tx_bytes = 0;

	// Allocate memory for the performance counters structure
	counters->counters_count = MODULE_ECTX_PERF_COUNTERS;
	counters->counters = (struct module_performance_counter *)malloc(
		sizeof(struct module_performance_counter) *
		MODULE_ECTX_PERF_COUNTERS
	);
	if (counters->counters == NULL) {
		yanet_counter_handle_list_free(counter_list);
		yanet_error_add(
			err,
			"failed to allocate memory for performance counters"
		);
		goto err;
	}

	// Initialize counters array to avoid uninitialized memory
	memset(counters->counters,
	       0,
	       sizeof(struct module_performance_counter) *
		       MODULE_ECTX_PERF_COUNTERS);

	// Parse all counters - both performance histograms and tx/rx counters
	for (size_t i = 0; i < counter_list->count; ++i) {
		struct counter_handle *counter_handle =
			yanet_get_counter(counter_list, i);
		if (counter_handle == NULL) {
			continue;
		}

		// Try parsing as performance counter (hist_0 through hist_5)
		struct module_performance_counter counter;
		size_t idx;
		int result = cp_module_parse_performance_counter(
			counter_handle,
			counter_list->instance_count,
			&idx,
			&counter
		);

		if (result == 0) {
			// Successfully parsed as performance counter
			counters->counters[idx] = counter;
		} else {
			// Not a performance counter, try parsing as tx/rx
			// counter
			result = cp_module_parse_tx_rx(
				counter_handle,
				counter_list->instance_count,
				&counters->tx,
				&counters->rx,
				&counters->tx_bytes,
				&counters->rx_bytes
			);

			if (result < 0) {
				// Error parsing tx/rx counter
				yanet_error_add(
					err,
					"failed to parse tx/rx counter '%s' "
					"for module '%s:%s'",
					counter_handle->name,
					module_type,
					module_name
				);
				// Clean up and return error
				for (size_t j = 0;
				     j < MODULE_ECTX_PERF_COUNTERS;
				     ++j) {
					free(counters->counters[j]
						     .latency_ranges);
				}
				free(counters->counters);
				yanet_counter_handle_list_free(counter_list);
				goto err;
			}
			// If result == 1, it's neither a performance counter
			// nor tx/rx counter (skip it) If result == 0, we
			// successfully parsed and populated the tx/rx fields
		}
	}

	yanet_counter_handle_list_free(counter_list);
	return 0;

err:
	return -1;
}

void
yanet_module_performance_counters_free(
	struct module_performance_counters *counters
) {
	if (counters == NULL) {
		return;
	}

	if (counters->counters != NULL) {
		for (size_t i = 0; i < counters->counters_count; ++i) {
			free(counters->counters[i].latency_ranges);
		}
		free(counters->counters);
	}
}
