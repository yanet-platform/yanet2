#include "agent.h"

#include <stdbool.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "common/memory_address.h"

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

// Checked accumulation for a list's value-region size.
//
// Every region term is added through this guard so a corrupted
// registry size cannot wrap the sum into a small allocation.
static bool
counter_region_add(size_t *sum, size_t add) {
	if (add > SIZE_MAX - *sum) {
		return false;
	}
	*sum += add;
	return true;
}

// Size of one tag-array copy: the fixed-size struct array alone.
static bool
counter_tag_region_size(size_t tag_count, size_t *out) {
	if (tag_count > SIZE_MAX / sizeof(struct counter_tag)) {
		return false;
	}
	*out = tag_count * sizeof(struct counter_tag);
	return true;
}

// Size of one group's handle array.
static bool
counter_handle_region_size(size_t handle_count, size_t *out) {
	if (handle_count > SIZE_MAX / sizeof(struct counter_handle)) {
		return false;
	}
	*out = handle_count * sizeof(struct counter_handle);
	return true;
}

// Carve a tag-array copy from the cursor and fill it in, advancing the
// cursor past it.
static struct counter_tag *
counter_tags_carve(
	uint8_t **cursor, const struct counter_tag *tags, size_t tag_count
) {
	struct counter_tag *dup = (struct counter_tag *)*cursor;
	*cursor += tag_count * sizeof(*dup);
	memcpy(dup, tags, tag_count * sizeof(*dup));
	return dup;
}

// Size of one counter's share of a list's value region: the counter's
// single value block.
static bool
counter_value_region_size(uint64_t counter_size, size_t *out) {
	if (counter_size > SIZE_MAX / sizeof(uint64_t)) {
		return false;
	}
	*out = (size_t)counter_size * sizeof(uint64_t);
	return true;
}

// Snapshot one already-described counter's values into a block carved
// from the cursor and point the handle at it.
//
// The values come from a single-instance storage. Worker storages are
// dataplane-owned and outlive every configuration generation, so no
// lock or generation pin is required around this call. A zero-size
// counter keeps the NULL block the zeroed allocation left behind.
static void
counter_handle_fill_values(
	struct counter_handle *dst,
	uint8_t **cursor,
	struct counter_storage *storage,
	uint64_t idx
) {
	if (dst->size == 0) {
		return;
	}
	uint64_t *values = (uint64_t *)*cursor;
	*cursor += dst->size * sizeof(uint64_t);
	dst->values = values;

	struct counter_value_handle *handle =
		counter_get_value_handle(idx, storage);
	memcpy(values,
	       counter_handle_get_value(handle),
	       dst->size * sizeof(uint64_t));
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

// Allocate a handle list sized for match_count handles plus a
// values_size-byte value region, and zero all of it.
//
// Sets count on success. The value region sits right after the handle
// array and is carved sequentially by the fill pass; the zeroing
// leaves every not-yet-carved handle with NULL values, and a later
// pass only overwrites what it fills in.
static struct counter_handle_list *
counter_handle_list_alloc(
	size_t match_count, size_t values_size, yanet_error **err
) {
	size_t list_size = sizeof(struct counter_handle_list) +
			   sizeof(struct counter_handle) * match_count;
	if (!counter_region_add(&list_size, values_size)) {
		yanet_error_add(err, "counter list size overflow");
		return NULL;
	}
	struct counter_handle_list *list =
		(struct counter_handle_list *)malloc(list_size);
	if (list == NULL) {
		yanet_error_add(err, "malloc failed");
		return NULL;
	}
	memset(list, 0, list_size);
	list->count = match_count;
	return list;
}

// The first byte of a list's value region: right past the handle
// array. The carve cursor starts here.
static uint8_t *
counter_handle_list_region(const struct counter_handle_list *list) {
	return (uint8_t *)(list->counters + list->count);
}

// Allocate a group list sized for group_count groups plus a
// region_size-byte carve region, and zero all of it.
//
// Sets group_count on success. The region sits right after the group
// array and is carved sequentially by the fill pass; the zeroing
// leaves every not-yet-carved handle with NULL values, and a later
// pass only overwrites what it fills in.
static struct counter_group_list *
counter_group_list_alloc(
	size_t group_count, size_t region_size, yanet_error **err
) {
	size_t list_size = sizeof(struct counter_group_list) +
			   sizeof(struct counter_group) * group_count;
	if (!counter_region_add(&list_size, region_size)) {
		yanet_error_add(err, "counter list size overflow");
		return NULL;
	}
	struct counter_group_list *list =
		(struct counter_group_list *)malloc(list_size);
	if (list == NULL) {
		yanet_error_add(err, "malloc failed");
		return NULL;
	}
	memset(list, 0, list_size);
	list->group_count = group_count;
	return list;
}

// The first byte of a group list's carve region: right past the group
// array. The carve cursor starts here.
static uint8_t *
counter_group_list_region(const struct counter_group_list *list) {
	return (uint8_t *)(list->groups + list->group_count);
}

// Build one worker's group list from its matching storages.
//
// matches is that worker's NULL-terminated storage match array, or
// NULL for a worker without an execution context. Every matched
// storage with at least one name-matching counter becomes one group
// stating the storage's tags once, holding its matched handles with
// values snapshotted from that worker's own storage only.
//
// The sizing pass records each storage's match count so the fill pass
// never re-runs the name matcher to reconstruct it; the generation pin
// held by the caller keeps the registries from changing between walks.
static struct counter_group_list *
worker_counter_group_list_build(
	struct cp_counter_storage **matches,
	const struct counter_pattern_set *names,
	yanet_error **err
) {
	if (matches == NULL) {
		return counter_group_list_alloc(0, 0, err);
	}

	size_t storage_count = 0;
	for (size_t i = 0; matches[i] != NULL; ++i) {
		++storage_count;
	}

	size_t *match_counts = NULL;
	if (storage_count != 0) {
		if (storage_count > SIZE_MAX / sizeof(*match_counts)) {
			yanet_error_add(err, "counter list size overflow");
			return NULL;
		}
		match_counts = malloc(storage_count * sizeof(*match_counts));
		if (match_counts == NULL) {
			yanet_error_add(err, "malloc failed");
			return NULL;
		}
	}

	size_t group_count = 0;
	size_t region_size = 0;
	for (size_t i = 0; matches[i] != NULL; ++i) {
		struct cp_counter_storage *cp_storage = matches[i];
		struct counter_storage *storage = ADDR_OF(&cp_storage->storage);
		struct counter_registry *registry = ADDR_OF(&storage->registry);
		struct counter *counters = ADDR_OF(&registry->names);

		size_t storage_matches = 0;
		size_t region = 0;
		for (uint64_t idx = 0; idx < registry->count; ++idx) {
			if (!counter_pattern_set_match(
				    names, counters[idx].name
			    )) {
				continue;
			}
			size_t counter_region;
			if (!counter_value_region_size(
				    counters[idx].size, &counter_region
			    ) ||
			    !counter_region_add(&region, counter_region)) {
				yanet_error_add(
					err, "counter list size overflow"
				);
				free(match_counts);
				return NULL;
			}
			++storage_matches;
		}

		match_counts[i] = storage_matches;
		if (storage_matches == 0) {
			continue;
		}

		size_t handles_region;
		size_t tags_region;
		if (!counter_handle_region_size(
			    storage_matches, &handles_region
		    ) ||
		    !counter_tag_region_size(
			    cp_storage->tag_count, &tags_region
		    ) ||
		    !counter_region_add(&region, handles_region) ||
		    !counter_region_add(&region, tags_region) ||
		    !counter_region_add(&region_size, region)) {
			yanet_error_add(err, "counter list size overflow");
			free(match_counts);
			return NULL;
		}
		++group_count;
	}

	struct counter_group_list *list =
		counter_group_list_alloc(group_count, region_size, err);
	if (list != NULL) {
		uint8_t *cursor = counter_group_list_region(list);
		size_t next_group = 0;
		for (size_t i = 0; matches[i] != NULL; ++i) {
			size_t storage_matches = match_counts[i];
			if (storage_matches == 0) {
				continue;
			}

			struct cp_counter_storage *cp_storage = matches[i];
			struct counter_storage *storage =
				ADDR_OF(&cp_storage->storage);
			struct counter_registry *registry =
				ADDR_OF(&storage->registry);
			struct counter *counters = ADDR_OF(&registry->names);

			struct counter_group *group = &list->groups[next_group];
			group->count = storage_matches;
			group->tags = counter_tags_carve(
				&cursor, cp_storage->tags, cp_storage->tag_count
			);
			group->tag_count = cp_storage->tag_count;
			group->counters = (struct counter_handle *)cursor;
			cursor += storage_matches * sizeof(*group->counters);

			size_t next = 0;
			for (uint64_t idx = 0; idx < registry->count; ++idx) {
				if (!counter_pattern_set_match(
					    names, counters[idx].name
				    )) {
					continue;
				}
				struct counter_handle *dst =
					&group->counters[next];
				const struct counter *src = &counters[idx];
				strtcpy(dst->name, src->name, sizeof(dst->name)
				);
				dst->size = src->size;
				dst->gen = src->gen;
				counter_handle_fill_values(
					dst, &cursor, storage, idx
				);
				++next;
			}
			++next_group;
		}
	}

	free(match_counts);
	return list;
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
		sets->sets[w_idx].groups = worker_counter_group_list_build(
			matches->by_worker[w_idx], names, err
		);
		if (sets->sets[w_idx].groups == NULL) {
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

// Release a group list owned by a worker set. No-op on NULL.
//
// The list's single allocation owns every group's handle array,
// tag-array copy and value block: they are carved out of its region,
// so one free reclaims all of it.
static void
counter_group_list_free(struct counter_group_list *groups) {
	if (groups == NULL) {
		return;
	}
	free(groups);
}

void
yanet_counter_worker_set_list_free(struct counter_worker_set_list *sets) {
	if (sets == NULL) {
		return;
	}
	for (uint64_t w_idx = 0; w_idx < sets->worker_count; ++w_idx) {
		counter_group_list_free(sets->sets[w_idx].groups);
	}
	free(sets);
}

struct counter_group *
yanet_get_counter_group(struct counter_group_list *groups, uint64_t idx) {
	if (groups == NULL || idx >= groups->group_count) {
		return NULL;
	}
	return groups->groups + idx;
}

struct counter_handle *
yanet_get_group_counter(struct counter_group *group, uint64_t idx) {
	if (group == NULL || idx >= group->count) {
		return NULL;
	}
	return group->counters + idx;
}

struct counter_handle *
yanet_get_counter(struct counter_handle_list *counters, uint64_t idx) {
	if (idx >= counters->count) {
		return NULL;
	}
	return counters->counters + idx;
}

uint64_t
yanet_get_counter_value(const uint64_t *values, uint64_t value_idx) {
	return values[value_idx];
}

void
yanet_get_counter_values(
	const uint64_t *values, uint64_t size, uint64_t *values_out
) {
	if (size == 0) {
		return;
	}
	memcpy(values_out, values, size * sizeof(uint64_t));
}

static struct counter_handle_list *
counter_handle_list_build(
	struct counter_registry *counter_registry,
	struct counter_storage *storage
) {
	uint64_t count = counter_registry->count;

	size_t values_size = 0;
	{
		struct counter *counters = ADDR_OF(&counter_registry->names);
		for (uint64_t idx = 0; idx < count; ++idx) {
			size_t region;
			if (!counter_value_region_size(
				    counters[idx].size, &region
			    ) ||
			    !counter_region_add(&values_size, region)) {
				return NULL;
			}
		}
	}

	struct counter_handle_list *list =
		counter_handle_list_alloc(count, values_size, NULL);
	if (list == NULL) {
		return NULL;
	}
	struct counter_handle *handlers = list->counters;

	uint8_t *cursor = counter_handle_list_region(list);
	for (uint64_t idx = 0; idx < count; ++idx) {
		struct counter *counters = ADDR_OF(&counter_registry->names);
		struct counter_handle *dst = &handlers[idx];
		strtcpy(dst->name, counters[idx].name, sizeof(dst->name));
		dst->size = counters[idx].size;
		dst->gen = counters[idx].gen;
		counter_handle_fill_values(dst, &cursor, storage, idx);
	}

	return list;
}

struct counter_handle_list *
yanet_get_worker_counters(struct dp_config *dp_config, uint64_t worker_idx) {
	if (worker_idx >= dp_config->worker_counter_storage_count) {
		return NULL;
	}
	struct counter_storage **storages =
		ADDR_OF(&dp_config->worker_counter_storages);
	return counter_handle_list_build(
		&dp_config->worker_counters, ADDR_OF(storages + worker_idx)
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
			&pc->registry, ADDR_OF(&pc->storage)
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
	// The list's single allocation owns every handle's value block:
	// it is carved out of its value region, so one free reclaims all
	// of it.
	free(counters);
}
