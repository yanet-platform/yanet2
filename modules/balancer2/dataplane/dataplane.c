#include <assert.h>
#include <netinet/in.h>
#include <rte_ether.h>
#include <stdio.h>
#include <stdlib.h>

#include "common/memory_address.h"

#include "lib/counters/counters.h"
#include "lib/dataplane/config/zone.h"
#include "lib/dataplane/module/packet_front.h"
#include "lib/dataplane/packet/data.h"
#include "lib/dataplane/pipeline/econtext.h"

#include "types/stats.h"

#include "config.h"
#include "context.h"
#include "dataplane.h"

#include "icmp/handle.h"
#include "l4/handle.h"

#define MAX_BATCH_SIZE 64
static_assert(
	MAX_BATCH_SIZE <= 256,
	"MAX_BATCH_SIZE must fit in a uint8_t index"
);

typedef void (*batch_handler)(
	struct worker_context *context,
	struct packet **packets,
	size_t packets_count
);

struct packet_batcher {
	struct packet *packets[MAX_BATCH_SIZE];
	size_t count;
	batch_handler handler;
};

static void
batcher_add(
	struct packet_batcher *batcher,
	struct worker_context *context,
	struct packet *packet
) {
	batcher->packets[batcher->count++] = packet;
	if (batcher->count == MAX_BATCH_SIZE) {
		batcher->handler(context, batcher->packets, batcher->count);
		batcher->count = 0;
	}
}

static void
batcher_flush(struct packet_batcher *batcher, struct worker_context *context) {
	if (batcher->count > 0) {
		batcher->handler(context, batcher->packets, batcher->count);
		batcher->count = 0;
	}
}

static uint64_t *
context_get_counter(struct worker_context *context, uint64_t counter_id) {
	return counter_get_address(
		counter_id, context->worker_idx, context->counter_storage
	);
}

static void
build_context(
	struct worker_context *ctx,
	struct dp_worker *dp_worker,
	struct module_ectx *module_ectx,
	struct packet_front *packet_front
) {
	struct balancer_module_config *config = container_of(
		ADDR_OF(&module_ectx->cp_module),
		struct balancer_module_config,
		cp_module
	);
	ctx->config = config;
	ctx->packet_front = packet_front;
	ctx->counter_storage = ADDR_OF(&module_ectx->counter_storage);
	ctx->worker_idx = dp_worker->idx;
	ctx->now = dp_worker->current_time / (1000 * 1000 * 1000); /* ns -> s */

	ctx->common_stats = (struct balancer_common_stats *)context_get_counter(
		ctx, config->common_counter_id
	);
	ctx->l4_stats = (struct balancer_l4_stats *)context_get_counter(
		ctx, config->l4_counter_id
	);
}

static inline bool
valid_packet_network(uint16_t network_type) {
	return network_type == rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4) ||
	       network_type == rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6);
}

static inline bool
valid_packet_transport(uint16_t transport_type) {
	/*
	 * ICMP is not load-balanced; only connection-bearing transport
	 * protocols are forwarded to reals.
	 */
	return transport_type == IPPROTO_TCP || transport_type == IPPROTO_UDP;
}

void
balancer2_handle_packets(
	struct dp_worker *dp_worker,
	struct module_ectx *module_ectx,
	struct packet_front *packet_front
) {
	struct worker_context context;
	build_context(&context, dp_worker, module_ectx, packet_front);

	/*
	 * Classify incoming packets by protocol (L4/ICMP) and IP version
	 * (IPv4/IPv6), accumulating them into per-category batches.
	 *
	 * Batched processing allows the filter engine to evaluate multiple
	 * packets at once, which is significantly faster than one-at-a-time
	 * lookups due to memory prefetching and reduced per-packet overhead.
	 *
	 * Batches are flushed when they reach MAX_BATCH_SIZE or when all
	 * input packets have been classified.
	 */
	enum { l4_ip4, l4_ip6, icmp_ip4, icmp_ip6, batcher_count };
	struct packet_batcher batchers[batcher_count] = {
		[l4_ip4] = {.handler = balancer_handle_l4_ip4},
		[l4_ip6] = {.handler = balancer_handle_l4_ip6},
		/* For the future ICMP support. */
		[icmp_ip4] = {.handler = balancer_handle_icmp_ip4},
		[icmp_ip6] = {.handler = balancer_handle_icmp_ip6},
	};

	context.common_stats->incoming_packets += packet_front->input.count;

	struct packet *packet;
	while ((packet = packet_list_pop(&packet_front->input)) != NULL) {
		context.common_stats->incoming_bytes += packet_data_len(packet);

		uint16_t network_type = packet->network_header.type;
		if (!valid_packet_network(network_type)) {
			context.common_stats->unexpected_network_proto += 1;
			packet_front_output(packet_front, packet);
			continue;
		}

		uint16_t transport_type = packet->transport_header.type;
		if (!valid_packet_transport(transport_type)) {
			context.common_stats->unexpected_transport_proto += 1;
			packet_front_output(packet_front, packet);
			continue;
		}

		int is_ip6 =
			network_type == rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6);
		int is_icmp = transport_type == IPPROTO_ICMP ||
			      transport_type == IPPROTO_ICMPV6;
		int idx = is_icmp * 2 + is_ip6;
		batcher_add(&batchers[idx], &context, packet);
	}

	for (int i = 0; i < batcher_count; ++i) {
		batcher_flush(&batchers[i], &context);
	}
}

struct balancer_module {
	struct module module;
};

struct module *
new_module_balancer2() {
	struct balancer_module *module =
		(struct balancer_module *)malloc(sizeof(struct balancer_module)
		);

	if (module == NULL) {
		return NULL;
	}

	snprintf(
		module->module.name,
		sizeof(module->module.name),
		"%s",
		"balancer2"
	);
	module->module.handler = balancer2_handle_packets;

	return &module->module;
}
