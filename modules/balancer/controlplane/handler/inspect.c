#include "inspect.h"
#include "api/inspect.h"
#include "api/stats.h"
#include "common/lpm.h"
#include "common/memory_address.h"
#include "compiler.h"
#include "map.h"
#include "registry.h"
#include "vs.h"
#include <stdlib.h>

void
packet_handler_vs_inspect(
	struct packet_handler_vs *handler_vs,
	struct packet_handler_vs_inspect *inspect,
	size_t workers
) {
	inspect->matcher_usage =
		filter_memory_usage(ADDR_OF(&handler_vs->filter));
	inspect->summary_vs_usage = 0;
	inspect->vs_count = handler_vs->vs_count;
	inspect->vs_inspects =
		malloc(sizeof(struct named_vs_inspect) * handler_vs->vs_count);
	struct vs *vs = ADDR_OF(&handler_vs->vs);
	for (size_t vs_idx = 0; vs_idx < handler_vs->vs_count; ++vs_idx) {
		struct named_vs_inspect *vs_inspect =
			inspect->vs_inspects + vs_idx;
		vs_inspect->identifier = vs[vs_idx].identifier;
		vs_fill_inspect(vs + vs_idx, &vs_inspect->inspect, workers);
		inspect->summary_vs_usage += vs_inspect->inspect.total_usage;
	}
	inspect->announce_usage = lpm_memory_usage(&handler_vs->announce);
	inspect->index_usage = map_memory_usage(&handler_vs->index);
	inspect->total_usage = inspect->matcher_usage +
			       inspect->summary_vs_usage +
			       inspect->announce_usage + inspect->index_usage;
}

void
packet_handler_inspect(
	struct packet_handler *handler,
	struct packet_handler_inspect *inspect,
	size_t workers
) {
	packet_handler_vs_inspect(
		&handler->vs_ipv4, &inspect->vs_ipv4_inspect, workers
	);
	packet_handler_vs_inspect(
		&handler->vs_ipv6, &inspect->vs_ipv6_inspect, workers
	);
	inspect->summary_vs_usage = inspect->vs_ipv4_inspect.summary_vs_usage +
				    inspect->vs_ipv6_inspect.summary_vs_usage +
				    sizeof(struct vs) * handler->vs_count;
	inspect->reals_index_usage =
		map_memory_usage(&handler->reals_index) +
		reals_registry_usage(&handler->reals_registry);
	inspect->vs_index_usage = map_memory_usage(&handler->vs_index) +
				  vs_registry_usage(&handler->vs_registry);
	inspect->counters_usage = (sizeof(struct balancer_icmp_stats) * 2 +
				   sizeof(struct balancer_common_stats) +
				   sizeof(struct balancer_l4_stats)) *
				  workers;
	inspect->decap_usage = lpm_memory_usage(&handler->decap_ipv4) +
			       lpm_memory_usage(&handler->decap_ipv6);
	inspect->total_usage = inspect->vs_ipv4_inspect.total_usage +
			       inspect->vs_ipv6_inspect.total_usage +
			       inspect->reals_index_usage +
			       inspect->vs_index_usage +
			       inspect->counters_usage + inspect->decap_usage;
}