#include "api/balancer.h"
#include "api/real.h"
#include "api/vs.h"
#include "common/memory_address.h"
#include "common/strutils.h"
#include "controlplane/agent/agent.h"
#include "handler.h"

#include "api/counter.h"

#include "lib/controlplane/diag/diag.h"
#include "vs.h"
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

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
	struct real_stats *real_stats,
	const size_t instances,
	struct counter_handle *counter
) {
	counter_handle_accum(
		(uint64_t *)real_stats,
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
setup_vs_acl_stats(
	struct allowed_sources_stats *stats,
	const char *tag,
	const size_t instances,
	struct counter_handle *counter
) {
	strtcpy(stats->tag, tag, MAX_TAG_LEN);
	counter_handle_accum(
		(uint64_t *)&stats->passes,
		instances,
		counter->size,
		counter->value_handle
	);
}

static void
inc_balancer_stats(
	struct balancer_common_stats *common_stats,
	struct balancer_l4_stats *l4_stats,
	struct balancer_icmp_stats *icmp_ipv4_stats,
	struct balancer_icmp_stats *icmp_ipv6_stats,
	const size_t workers,
	struct counter_handle *counter
) {
	if (strcmp(counter->name, common_module_counter_name) ==
	    0) { // common module counter
		counter_handle_accum(
			(uint64_t *)common_stats,
			workers,
			counter->size,
			counter->value_handle
		);
	} else if (strcmp(counter->name, icmp_v4_module_counter_name) ==
		   0) { // icmp module counter
		counter_handle_accum(
			(uint64_t *)icmp_ipv4_stats,
			workers,
			counter->size,
			counter->value_handle
		);
	} else if (strcmp(counter->name, icmp_v6_module_counter_name) == 0) {
		counter_handle_accum(
			(uint64_t *)icmp_ipv6_stats,
			workers,
			counter->size,
			counter->value_handle
		);
	} else if (strcmp(counter->name, l4_module_counter_name) ==
		   0) { // l4 module counter
		counter_handle_accum(
			(uint64_t *)l4_stats,
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
		real_stats[i].real = reals[i].identifier.relative;
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
		struct allowed_sources_stats *stats =
			malloc(sizeof(struct allowed_sources_stats) *
			       vs->rules_count);
		vs_stats->allowed_sources = stats;
		vs_stats->allowed_sources_count = 0;
	}
}

static void
calculate_stats(
	struct packet_handler *handler,
	struct balancer_common_stats *common_stats,
	struct balancer_l4_stats *l4_stats,
	struct balancer_icmp_stats *icmp_ipv4_stats,
	struct balancer_icmp_stats *icmp_ipv6_stats,
	struct named_vs_stats *vs_stats_list,
	struct named_vs_snapshot *vs_snapshot,
	struct named_real_stats *real_stats_list,
	struct named_real_snapshot *reals_snapshot,
	struct counter_handle_list *counter_handles,
	bool use_snapshot,
	bool vs_acl
) {
	const size_t instances = counter_handles->instance_count;

	// calculate virtual service, real and common balancer stats

	for (size_t i = 0; i < counter_handles->count; ++i) {
		struct counter_handle *counter = &counter_handles->counters[i];
		ssize_t vs_stable_idx = counter_to_vs_registry_idx(counter);
		if (vs_stable_idx != -1) {
			size_t vs_config_idx;
			if (map_find(
				    &handler->vs_index,
				    vs_stable_idx,
				    &vs_config_idx
			    ) != 0) {
				// virtual service not present in packet handler
				// config
				continue;
			}
			struct vs_stats *vs_stats =
				use_snapshot
					? &vs_snapshot[vs_config_idx]
						   .snapshot.stats
					: &vs_stats_list[vs_config_idx].stats;
			setup_vs_stats(vs_stats, instances, counter);
			continue;
		}

		// else, if it is not virtual service counter
		// check if it is real counter

		ssize_t real_stable_idx = counter_to_real_registry_idx(counter);
		if (real_stable_idx != -1) {
			size_t real_config_idx;
			if (map_find(
				    &handler->reals_index,
				    real_stable_idx,
				    &real_config_idx
			    ) != 0) {
				// real not present in packet handler config
				continue;
			}
			struct real_stats *real_stats =
				use_snapshot ? &reals_snapshot[real_config_idx]
							.snapshot.stats
					     : &real_stats_list[real_config_idx]
							.stats;
			setup_real_stats(real_stats, instances, counter);
			continue;
		}

		if (vs_acl) {
			const char *rule_tag;
			vs_stable_idx =
				parse_vs_acl_counter(counter, &rule_tag);
			if (vs_stable_idx != -1) {
				size_t vs_config_idx;
				if (map_find(
					    &handler->vs_index,
					    vs_stable_idx,
					    &vs_config_idx
				    ) != 0) {
					// virtual service not present in packet
					// handler config
					continue;
				}

				// work with allowed sources stats
				struct allowed_sources_stats *stats;
				if (use_snapshot) {
					struct named_vs_snapshot *vs =
						&vs_snapshot[vs_config_idx];
					size_t allowed_sources_scount =
						vs[vs_config_idx]
							.snapshot
							.allowed_sources_count++;
					stats = &vs->snapshot.allowed_sources
							 [allowed_sources_scount];
				} else {
					struct named_vs_stats *vs_stats =
						&vs_stats_list[vs_config_idx];
					size_t allowed_sources_scount =
						vs_stats->allowed_sources_count++;
					stats = &vs_stats->allowed_sources
							 [allowed_sources_scount];
				}

				setup_vs_acl_stats(
					stats, rule_tag, instances, counter
				);
			}
		}

		// else, it is common balancer counter
		inc_balancer_stats(
			common_stats,
			l4_stats,
			icmp_ipv4_stats,
			icmp_ipv6_stats,
			instances,
			counter
		);
	}
}

int
packet_handler_fill_snapshot_stats(
	struct packet_handler *handler,
	struct balancer_snapshot *snapshot,
	struct packet_handler_ref *ref,
	bool vs_acl
) {
	if (ref->device == NULL || ref->pipeline == NULL ||
	    ref->function == NULL || ref->chain == NULL) {
		NEW_ERROR("invalid packet handler reference");
		return -1;
	}

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
		module,
		NULL,
		(size_t)-1
	);
	if (counter_handles == NULL) {
		NEW_ERROR("failed to find stats");
		return -1;
	}

	calculate_stats(
		handler,
		&snapshot->common_stats,
		&snapshot->l4_stats,
		&snapshot->icmp_ipv4_stats,
		&snapshot->icmp_ipv6_stats,
		NULL,
		snapshot->vs_snapshots,
		NULL,
		snapshot->vs_snapshots
			? snapshot->vs_snapshots[0].snapshot.reals
			: NULL,
		counter_handles,
		true,
		vs_acl
	);

	free(counter_handles);

	return 0;
}

int
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
		module,
		NULL,
		(size_t)-1
	);
	if (counter_handles == NULL) {
		NEW_ERROR("failed to find stats");
		return -1;
	}

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
	calculate_stats(
		handler,
		&stats->common,
		&stats->l4,
		&stats->icmp_ipv4,
		&stats->icmp_ipv6,
		stats->vs,
		NULL,
		real_stats,
		NULL,
		counter_handles,
		false,
		true
	);

	return 0;
}