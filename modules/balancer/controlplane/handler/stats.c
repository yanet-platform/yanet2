#include "api/balancer.h"
#include "api/real.h"
#include "common/memory_address.h"
#include "controlplane/agent/agent.h"
#include "handler.h"

#include "api/counter.h"

#include "lib/controlplane/diag/diag.h"
#include "vs.h"

#include "api/stats.h"

////////////////////////////////////////////////////////////////////////////////

const char *common_module_counter_name = "cmn";
const char *icmp_v4_module_counter_name = "iv4";
const char *icmp_v6_module_counter_name = "iv6";
const char *l4_module_counter_name = "l4";

////////////////////////////////////////////////////////////////////////////////

uint64_t
register_common_counter(struct counter_registry *registry) {
	uint64_t res = counter_registry_register(
		registry,
		common_module_counter_name,
		sizeof(struct balancer_common_stats) / sizeof(uint64_t)
	);

	if (res == (uint64_t)-1) {
		PUSH_ERROR("failed to register counter in registry");
		return -1;
	}

	return res;
}

uint64_t
register_icmp_v4_counter(struct counter_registry *registry) {
	uint64_t res = counter_registry_register(
		registry,
		icmp_v4_module_counter_name,
		sizeof(struct balancer_icmp_stats) / sizeof(uint64_t)
	);

	if (res == (uint64_t)-1) {
		PUSH_ERROR("failed to register counter in registry");
		return -1;
	}

	return res;
}

uint64_t
register_icmp_v6_counter(struct counter_registry *registry) {
	uint64_t res = counter_registry_register(
		registry,
		icmp_v6_module_counter_name,
		sizeof(struct balancer_icmp_stats) / sizeof(uint64_t)
	);

	if (res == (uint64_t)-1) {
		PUSH_ERROR("failed to register counter in registry");
		return -1;
	}

	return res;
}

uint64_t
register_l4_counter(struct counter_registry *registry) {
	uint64_t res = counter_registry_register(
		registry,
		l4_module_counter_name,
		sizeof(struct balancer_l4_stats) / sizeof(uint64_t)
	);

	if (res == (uint64_t)-1) {
		PUSH_ERROR("failed to register counter in registry");
		return -1;
	}

	return res;
}

////////////////////////////////////////////////////////////////////////////////

static void
setup_real_stats(
	struct named_real_stats *real_stats,
	const size_t instances,
	struct counter_handle *counter
) {
	counter_handle_accum(
		(uint64_t *)&real_stats->stats,
		instances,
		counter->size,
		counter->value_handle
	);
}

static void
setup_vs_stats(
	struct vs_stats *stats,
	const size_t instances,
	struct counter_handle *counter
) {
	counter_handle_accum(
		(uint64_t *)stats,
		instances,
		counter->size,
		counter->value_handle
	);
}

static void
inc_balancer_stats(
	struct balancer_stats *stats,
	const size_t workers,
	struct counter_handle *counter
) {
	if (strcmp(counter->name, common_module_counter_name) ==
	    0) { // common module counter
		counter_handle_accum(
			(uint64_t *)&stats->common,
			workers,
			counter->size,
			counter->value_handle
		);
	} else if (strcmp(counter->name, icmp_v4_module_counter_name) ==
		   0) { // icmp module counter
		counter_handle_accum(
			(uint64_t *)&stats->icmp_ipv4,
			workers,
			counter->size,
			counter->value_handle
		);
	} else if (strcmp(counter->name, icmp_v6_module_counter_name) == 0) {
		counter_handle_accum(
			(uint64_t *)&stats->icmp_ipv6,
			workers,
			counter->size,
			counter->value_handle
		);
	} else if (strcmp(counter->name, l4_module_counter_name) ==
		   0) { // l4 module counter
		counter_handle_accum(
			(uint64_t *)&stats->l4,
			workers,
			counter->size,
			counter->value_handle
		);
	}
}

static void
init_real_stats(
	size_t reals_count,
	struct named_real_stats *real_stats,
	struct real *reals
) {
	for (size_t i = 0; i < reals_count; ++i) {
		real_stats[i].real = reals[i].identifier;
		memset(&real_stats[i].stats, 0, sizeof(struct real_stats));
	}
}

static void
init_vs_stats(
	struct packet_handler *handler,
	struct balancer_stats *stats,
	struct named_real_stats *real_stats
) {
	stats->vs_count = handler->vs_count;
	stats->vs = malloc(sizeof(struct named_vs_stats) * stats->vs_count);

	// init virtual services
	struct vs *vss = ADDR_OF(&handler->vs);
	size_t reals_counter = 0;
	for (size_t i = 0; i < stats->vs_count; ++i) {
		struct named_vs_stats *vs_stats = &stats->vs[i];
		struct vs *vs = &vss[i];
		vs_stats->identifier = vs->identifier;
		memset(&vs_stats->stats, 0, sizeof(struct vs_stats));
		vs_stats->reals_count = vs->reals_count;
		vs_stats->reals = real_stats + reals_counter;
		reals_counter += vs->reals_count;
	}
}

static void
calculate_stats(
	struct packet_handler *handler,
	struct balancer_stats *stats,
	struct named_real_stats *real_stats,
	struct counter_handle_list *counter_handles
) {
	// vs index and reals index
	uint32_t *vs_index = ADDR_OF(&handler->vs_index);
	uint32_t *reals_index = ADDR_OF(&handler->reals_index);

	const size_t instances = counter_handles->instance_count;

	// calculate virtual service, real and common balancer stats

	for (size_t i = 0; i < counter_handles->count; ++i) {
		struct counter_handle *counter = &counter_handles->counters[i];
		ssize_t vs_registry_idx = counter_to_vs_registry_idx(counter);
		if (vs_registry_idx != -1) {
			uint32_t idx = vs_index[vs_registry_idx];
			if (idx == INDEX_INVALID) {
				// virtual service not present in packet handler
				// config, continue
				continue;
			}
			struct named_vs_stats *vs_stats = &stats->vs[idx];
			setup_vs_stats(&vs_stats->stats, instances, counter);
			continue;
		}

		// else, if it is not virtual service counter
		// check if it is real counter

		ssize_t real_registry_idx =
			counter_to_real_registry_idx(counter);
		if (real_registry_idx != -1) {
			uint32_t idx = reals_index[real_registry_idx];
			if (idx == (uint32_t)-1) {
				// real not present in packet handler config,
				// continue
				continue;
			}
			setup_real_stats(&real_stats[idx], instances, counter);
			continue;
		}

		// else, it is common balancer counter
		inc_balancer_stats(stats, instances, counter);
	}
}

void
packet_handler_fill_stats(
	struct packet_handler *handler,
	struct balancer_stats *stats,
	struct packet_handler_ref *ref
) {
	struct agent *agent = ADDR_OF(&handler->cp_module.agent);
	struct dp_config *dp_config = ADDR_OF(&agent->dp_config);

	const char *module = handler->cp_module.name;

	struct counter_handle_list *counter_handles = yanet_get_module_counters(
		dp_config,
		ref->device,
		ref->pipeline,
		ref->function,
		ref->chain,
		"balancer",
		module
	);
	assert(counter_handles != NULL);

	// Initialize all stats to zero
	memset(&stats->common, 0, sizeof(struct balancer_common_stats));
	memset(&stats->icmp_ipv4, 0, sizeof(struct balancer_icmp_stats));
	memset(&stats->icmp_ipv6, 0, sizeof(struct balancer_icmp_stats));
	memset(&stats->l4, 0, sizeof(struct balancer_l4_stats));

	// init real stats
	struct real *reals = ADDR_OF(&handler->reals);

	// layout of reals corresponds to the
	// layout in packet handler
	struct named_real_stats *real_stats =
		malloc(sizeof(struct named_real_stats) * handler->reals_count);

	init_real_stats(handler->reals_count, real_stats, reals);

	// init vs stats
	init_vs_stats(handler, stats, real_stats);

	// calculate stats
	calculate_stats(handler, stats, real_stats, counter_handles);
}