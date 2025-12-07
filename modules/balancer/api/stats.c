#include "stats.h"
#include "api/counter.h"
#include "common/memory.h"
#include "common/memory_address.h"

#include "counters/counters.h"
#include "lib/controlplane/agent/agent.h"
#include <assert.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

////////////////////////////////////////////////////////////////////////////////

static const char *common_module_counter_name = "common_counter";
static const char *icmp_v4_module_counter_name = "icmp_v4_counter";
static const char *icmp_v6_module_counter_name = "icmp_v6_counter";
static const char *l4_module_counter_name = "l4_counter";

////////////////////////////////////////////////////////////////////////////////
// Module counters
////////////////////////////////////////////////////////////////////////////////

uint64_t
register_common_counter(struct counter_registry *registry) {
	return counter_registry_register(
		registry,
		common_module_counter_name,
		sizeof(struct balancer_common_module_stats) / sizeof(uint64_t)
	);
}

uint64_t
register_icmp_v4_counter(struct counter_registry *registry) {
	return counter_registry_register(
		registry,
		icmp_v4_module_counter_name,
		sizeof(struct balancer_icmp_module_stats) / sizeof(uint64_t)
	);
}

uint64_t
register_icmp_v6_counter(struct counter_registry *registry) {
	return counter_registry_register(
		registry,
		icmp_v6_module_counter_name,
		sizeof(struct balancer_icmp_module_stats) / sizeof(uint64_t)
	);
}

uint64_t
register_l4_counter(struct counter_registry *registry) {
	return counter_registry_register(
		registry,
		l4_module_counter_name,
		sizeof(struct balancer_l4_module_stats) / sizeof(uint64_t)
	);
}

////////////////////////////////////////////////////////////////////////////////
// VS and Real counters
////////////////////////////////////////////////////////////////////////////////

uint64_t
register_vs_counter(struct counter_registry *registry, size_t vs_registry_idx) {
	char name[60];
	sprintf(name, "vs_%zu", vs_registry_idx);
	return counter_registry_register(
		registry,
		name,
		sizeof(struct balancer_vs_stats) / sizeof(uint64_t)
	);
}

uint64_t
register_real_counter(
	struct counter_registry *registry, size_t real_registry_idx
) {
	char name[60];
	sprintf(name, "rl_%zu", real_registry_idx);
	return counter_registry_register(
		registry,
		name,
		sizeof(struct balancer_real_stats) / sizeof(uint64_t)
	);
}

////////////////////////////////////////////////////////////////////////////////
// Balancer stats
////////////////////////////////////////////////////////////////////////////////

static inline void
fill_module_counters(
	struct balancer_stats_info *stats_info,
	const size_t instances,
	struct counter_handle *counter,
	size_t *vs_count,
	size_t *real_count
) {
	struct balancer_stats *stats = &stats_info->stats;
	if (strcmp(counter->name, common_module_counter_name) ==
	    0) { // common module counter
		counter_handle_accum(
			(uint64_t *)&stats->common,
			instances,
			counter->size,
			counter->value_handle
		);
	} else if (strcmp(counter->name, icmp_v4_module_counter_name) ==
		   0) { // icmp module counter
		counter_handle_accum(
			(uint64_t *)&stats->icmp_ipv4,
			instances,
			counter->size,
			counter->value_handle
		);
	} else if (strcmp(counter->name, icmp_v6_module_counter_name) == 0) {
		counter_handle_accum(
			(uint64_t *)&stats->icmp_ipv6,
			instances,
			counter->size,
			counter->value_handle
		);
	} else if (strcmp(counter->name, l4_module_counter_name) ==
		   0) { // l4 module counter
		counter_handle_accum(
			(uint64_t *)&stats->l4,
			instances,
			counter->size,
			counter->value_handle
		);
	} else if (strncmp(counter->name, "vs_", 3) == 0) { // vs counter
		++*vs_count;
	} else if (strncmp(counter->name, "rl_", 3) == 0) { // real counter
		++*real_count;
	}
}

static inline void
fill_vs_and_real_counters(
	struct balancer_stats_info *stats,
	const size_t instances,
	struct counter_handle *counter,
	size_t *vs_idx,
	size_t *real_idx
) {
	if (strncmp(counter->name, "vs_", 3) == 0) { // vs counter
		size_t vs_registry_idx = atoi(counter->name + 3);
		struct balancer_vs_stats_info *info =
			&stats->vs_info[*vs_idx++];
		info->vs_registry_idx = vs_registry_idx;
		counter_handle_accum(
			(uint64_t *)&info->stats,
			instances,
			counter->size,
			counter->value_handle
		);
	} else if (strncmp(counter->name, "rl_", 3) == 0) { // real counter
		size_t real_registry_idx = atoi(counter->name + 3);
		struct balancer_real_stats_info *info =
			&stats->real_info[*real_idx++];
		info->real_registry_idx = real_registry_idx;
		counter_handle_accum(
			(uint64_t *)&info->stats,
			instances,
			counter->size,
			counter->value_handle
		);
	}
}

////////////////////////////////////////////////////////////////////////////////

int
balancer_stats_info_fill(
	struct balancer_stats_info *stats,
	struct agent *agent,
	const char *device,
	const char *pipeline,
	const char *function,
	const char *chain,
	const char *module
) {
	assert(device != NULL);
	assert(pipeline != NULL);
	assert(function != NULL);
	assert(chain != NULL);
	assert(module != NULL);

	struct dp_config *dp_config = ADDR_OF(&agent->dp_config);
	struct counter_handle_list *counter_handles = yanet_get_module_counters(
		dp_config, device, pipeline, function, chain, "balancer", module
	);
	if (counter_handles == NULL) {
		return -1;
	}

	const size_t instances = counter_handles->instance_count;

	// find common, icmp and l4 module counters
	// also, calculate number of vs and real counters.

	size_t vs_count = 0;
	size_t real_count = 0;

	for (size_t i = 0; i < counter_handles->count; ++i) {
		struct counter_handle *counter = &counter_handles->counters[i];
		fill_module_counters(
			stats, instances, counter, &vs_count, &real_count
		);
	}

	// allocate vs and real info

	struct memory_context *mctx = &agent->memory_context;

	// allocate vs info
	stats->vs_count = vs_count;
	stats->vs_info = memory_balloc(
		mctx, sizeof(struct balancer_vs_stats) * vs_count
	);
	if (stats->vs_info == NULL) {
		return -1;
	}

	// allocate real info
	stats->real_count = real_count;
	stats->real_info = memory_balloc(
		mctx, sizeof(struct balancer_real_stats) * real_count
	);
	if (stats->real_info == NULL) {
		memory_bfree(
			mctx,
			stats->vs_info,
			sizeof(struct balancer_vs_stats) * vs_count
		);
		return -1;
	}

	// fill stats
	size_t vs_idx = 0;
	size_t real_idx = 0;
	for (size_t i = 0; i < counter_handles->count; ++i) {
		struct counter_handle *counter = &counter_handles->counters[i];
		fill_vs_and_real_counters(
			stats, instances, counter, &vs_idx, &real_idx
		);
	}

	// successfully filled stats

	return 0;
}

////////////////////////////////////////////////////////////////////////////////

void
balancer_stats_info_free(
	struct balancer_stats_info *stats, struct agent *agent
) {
	struct memory_context *mctx = &agent->memory_context;
	memory_bfree(
		mctx,
		stats->vs_info,
		stats->vs_count * sizeof(struct balancer_vs_stats)
	);
	memory_bfree(
		mctx,
		stats->real_info,
		stats->real_count * sizeof(struct balancer_real_stats)
	);
}

////////////////////////////////////////////////////////////////////////////////

static_assert(
	sizeof(struct balancer_vs_stats) / sizeof(uint64_t) <= 16,
	"too big vs counter"
);
static_assert(
	sizeof(struct balancer_real_stats) / sizeof(uint64_t) <= 16,
	"too big real counter"
);
static_assert(
	sizeof(struct balancer_common_module_stats) / sizeof(uint64_t) <= 16,
	"too big common module counter"
);
static_assert(
	sizeof(struct balancer_icmp_module_stats) / sizeof(uint64_t) <= 16,
	"too big icmp module counter"
);
static_assert(
	sizeof(struct balancer_l4_module_stats) / sizeof(uint64_t) <= 16,
	"too big l4 module counter"
);