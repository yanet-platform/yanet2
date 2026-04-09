#include <netinet/in.h>
#include <rte_ether.h>
#include <stdio.h>
#include <stdlib.h>

#include "common/memory_address.h"

#include "lib/counters/counters.h"
#include "lib/dataplane/config/zone.h"
#include "lib/dataplane/module/packet_front.h"
#include "lib/dataplane/pipeline/econtext.h"

#include "types/stats.h"

#include "context.h"
#include "dataplane.h"

#include "icmp/handle.h"
#include "l4/handle.h"

#define MAX_BATCH_SIZE 64

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
	return counter_get_address(counter_id, context->worker_idx, context->counter_storage);
}

static void
build_context(
	struct worker_context *ctx,
	struct dp_worker *dp_worker,
	struct module_ectx *module_ectx,
	struct packet_front *packet_front
) {
	struct balancer_packet_handler *packet_handler = container_of(
		ADDR_OF(&module_ectx->cp_module),
		struct balancer_packet_handler,
		cp_module
	);
	ctx->packet_handler = packet_handler;
	ctx->packet_front = packet_front;
	ctx->counter_storage = ADDR_OF(&module_ectx->counter_storage);
	ctx->worker_idx = dp_worker->idx;
	ctx->now = dp_worker->current_time / (1000 * 1000 * 1000); /* ns -> s */

	ctx->common_stats = (struct balancer_common_stats *)context_get_counter(ctx, packet_handler->common_counter_id);
	ctx->icmp_v4_stats = (struct balancer_icmp_stats *)context_get_counter(ctx, packet_handler->icmp_v4_counter_id);
	ctx->icmp_v6_stats = (struct balancer_icmp_stats *)context_get_counter(ctx, packet_handler->icmp_v6_counter_id);
	ctx->l4_stats = (struct balancer_l4_stats *)context_get_counter(ctx, packet_handler->l4_counter_id);
}

void
balancer_handle_packets(
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
	enum { l4_ipv4, l4_ipv6, icmp_ipv4, icmp_ipv6, batcher_count };
	struct packet_batcher batchers[batcher_count] = {
		[l4_ipv4] = {.handler = balancer_handle_l4_ipv4},
		[l4_ipv6] = {.handler = balancer_handle_l4_ipv6},
		[icmp_ipv4] = {.handler = balancer_handle_icmp_ipv4},
		[icmp_ipv6] = {.handler = balancer_handle_icmp_ipv6},
	};

	context.common_stats->incoming_packets += packet_front->input.count;

	struct packet *packet;
	while ((packet = packet_list_pop(&packet_front->input)) != NULL) {
		context.common_stats->incoming_bytes += packet->mbuf->pkt_len;

		int is_ipv6 = packet->network_header.type ==
			      rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6);
		int is_icmp = packet->transport_header.type == IPPROTO_ICMP ||
			      packet->transport_header.type == IPPROTO_ICMPV6;
		int idx = is_icmp * 2 + is_ipv6;
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
new_module_balancer() {
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
		"balancer"
	);
	module->module.handler = balancer_handle_packets;

	return &module->module;
}
