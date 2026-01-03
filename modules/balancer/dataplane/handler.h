#pragma once

#include "handler/handler.h"

static inline struct balancer_common_stats *
common_handler_counter(
	struct packet_handler *handler,
	size_t worker,
	struct counter_storage *storage
) {
	uint64_t *counter =
		counter_get_address(handler->counter.common, worker, storage);
	return (struct balancer_common_stats *)counter;
}

static inline struct balancer_icmp_stats *
icmp_v4_handler_counter(
	struct packet_handler *handler,
	size_t worker,
	struct counter_storage *storage
) {
	uint64_t *counter =
		counter_get_address(handler->counter.icmp_v4, worker, storage);
	return (struct balancer_icmp_stats *)counter;
}

static inline struct balancer_icmp_stats *
icmp_v6_handler_counter(
	struct packet_handler *handler,
	size_t worker,
	struct counter_storage *storage
) {
	uint64_t *counter =
		counter_get_address(handler->counter.icmp_v6, worker, storage);
	return (struct balancer_icmp_stats *)counter;
}

static inline struct balancer_l4_stats *
l4_handler_counter(
	struct packet_handler *handler,
	size_t worker,
	struct counter_storage *storage
) {
	uint64_t *counter =
		counter_get_address(handler->counter.l4, worker, storage);
	return (struct balancer_l4_stats *)counter;
}