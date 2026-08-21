#include "agent.h"

#include <stdio.h>
#include <stdlib.h>
#include <string.h>

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

static struct counter_tag *
cp_counter_storage_copy_tags(const struct cp_counter_storage *storage) {
	struct counter_tag *tags = malloc(storage->tag_count * sizeof(*tags));
	if (tags == NULL) {
		return NULL;
	}
	for (size_t i = 0; i < storage->tag_count; ++i) {
		tags[i].key = NULL;
		tags[i].value = NULL;
	}
	for (size_t i = 0; i < storage->tag_count; ++i) {
		tags[i].key = strdup(storage->tags[i].key);
		tags[i].value = strdup(storage->tags[i].value);
		if (tags[i].key == NULL || tags[i].value == NULL) {
			for (size_t j = 0; j < storage->tag_count; ++j) {
				free((void *)tags[j].key);
				free((void *)tags[j].value);
			}
			free(tags);
			return NULL;
		}
	}
	return tags;
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
// a worker without an execution context. Every worker registry is built
// by the same execution-context traversal, so the match order is
// identical across workers: for match index i, every worker's array
// names the same tag set at that index.
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

// Bookkeeping needed to find a matched counter's per-worker storages again
// during the value-copy pass, since a handle list keeps only a counter's
// name, size, generation and tags.
struct matched_counter {
	size_t m_idx;
	uint64_t r_idx;
};

// Allocate a handle list sized for match_count handles and zero it.
//
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

// Count the matches, allocate the handle list, and fill every handle's
// name, size, generation and tags.
//
// Leaves every handle's value snapshot unset, which a later pass fills
// in. On success, fills out_sources with one entry per handle, in the same
// order as the list, for the value-copy pass to consume.
static struct counter_handle_list *
counter_handle_list_build_metadata(
	const struct worker_counter_matches *matches,
	const struct counter_pattern_set *names,
	struct matched_counter **out_sources,
	yanet_error **err
) {
	// A worker set with no execution context yet (the initial generation
	// before any install) yields an empty list.
	if (matches->worker_count == 0 || matches->by_worker[0] == NULL) {
		*out_sources = NULL;
		return counter_handle_list_alloc(matches->worker_count, 0, err);
	}
	struct cp_counter_storage **matches0 = matches->by_worker[0];

	size_t match_count = 0;
	for (size_t i = 0; matches0[i] != NULL; ++i) {
		struct counter_storage *storage =
			ADDR_OF(&matches0[i]->storage);
		match_count += counter_registry_match_count(
			ADDR_OF(&storage->registry), names
		);
	}

	struct counter_handle_list *list = counter_handle_list_alloc(
		matches->worker_count, match_count, err
	);
	if (list == NULL) {
		return NULL;
	}

	struct matched_counter *sources = NULL;
	if (match_count > 0) {
		sources = malloc(match_count * sizeof(*sources));
		if (sources == NULL) {
			yanet_error_add(err, "malloc failed");
			free(list);
			return NULL;
		}
	}

	size_t next = 0;
	for (size_t i = 0; matches0[i] != NULL; ++i) {
		struct cp_counter_storage *cp_storage = matches0[i];
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
					free(sources);
					yanet_counter_handle_list_free(list);
					return NULL;
				}
			}
			struct counter_handle *dst = &list->counters[next];
			const struct counter *src = &counters[idx];
			strtcpy(dst->name, src->name, sizeof(dst->name));
			dst->size = src->size;
			dst->gen = src->gen;
			dst->tags = storage_tags;
			dst->tag_count = cp_storage->tag_count;
			sources[next] = (struct matched_counter){
				.m_idx = i,
				.r_idx = idx,
			};
			++next;
		}
	}

	*out_sources = sources;
	return list;
}

// Copy every matched counter's per-worker value snapshot into the list
// built by the metadata pass.
static int
counter_handle_list_fill_values(
	struct counter_handle_list *list,
	const struct worker_counter_matches *matches,
	const struct matched_counter *sources,
	yanet_error **err
) {
	uint64_t worker_count = matches->worker_count;
	struct counter_storage **worker_storages =
		worker_count > 0
			? malloc(worker_count * sizeof(*worker_storages))
			: NULL;
	if (worker_count > 0 && worker_storages == NULL) {
		yanet_error_add(err, "malloc failed");
		return -1;
	}

	for (uint64_t k = 0; k < list->count; ++k) {
		size_t m_idx = sources[k].m_idx;
		uint64_t r_idx = sources[k].r_idx;
		for (uint64_t w_idx = 0; w_idx < worker_count; ++w_idx) {
			struct cp_counter_storage *cps =
				matches->by_worker[w_idx][m_idx];
			worker_storages[w_idx] = ADDR_OF(&cps->storage);
		}
		if (counter_handle_copy_values(
			    &list->counters[k],
			    worker_storages,
			    worker_count,
			    r_idx
		    )) {
			yanet_error_add(err, "malloc failed");
			free(worker_storages);
			return -1;
		}
	}
	free(worker_storages);
	return 0;
}

// Match the tag and name predicates against the active configuration generation
// and return every matching counter's metadata and per-worker values.
//
// The config lock is held only long enough to acquire the generation pin
// at the start and to drop it again at the end. The pin itself stays held
// for the whole call: matching, allocation and the value copy all run
// unlocked against the pinned generation, so a concurrent config update
// can retire and replace it mid-call without disturbing this read.
struct counter_handle_list *
yanet_get_counters_by_tags(
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
	struct counter_handle_list *list = NULL;
	struct matched_counter *sources = NULL;

	if (worker_counter_matches_collect(
		    config_gen, worker_count, tags, tag_count, &matches, err
	    )) {
		goto out;
	}

	list = counter_handle_list_build_metadata(
		&matches, names, &sources, err
	);
	if (list == NULL) {
		goto out;
	}

	if (counter_handle_list_fill_values(list, &matches, sources, err)) {
		yanet_counter_handle_list_free(list);
		list = NULL;
		goto out;
	}

out:
	free(sources);
	worker_counter_matches_free(&matches);

	cp_config_lock(cp_config);
	cp_config_gen_release(cp_config, config_gen);
	cp_config_unlock(cp_config);

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
