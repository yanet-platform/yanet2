#include "fib.h"

#include <string.h>

#include "common/exp_array.h"
#include "common/memory_address.h"

int
route_fib_init(struct route_fib *fib, struct memory_context *memory_context) {
	if (lpm_init(&fib->lpm_v4, memory_context, "lpm_v4")) {
		return -1;
	}
	if (lpm_init(&fib->lpm_v6, memory_context, "lpm_v6")) {
		lpm_free(&fib->lpm_v4);
		return -1;
	}

	fib->route_count = 0;
	fib->routes = NULL;

	fib->route_list_count = 0;
	fib->route_lists = NULL;

	fib->route_index_count = 0;
	fib->route_indexes = NULL;

	return 0;
}

void
route_fib_fini(struct route_fib *fib, struct memory_context *memory_context) {
	struct route *routes = ADDR_OF(&fib->routes);
	mem_array_free_exp(
		memory_context, routes, sizeof(*routes), fib->route_count
	);

	struct route_list *route_lists = ADDR_OF(&fib->route_lists);
	mem_array_free_exp(
		memory_context,
		route_lists,
		sizeof(*route_lists),
		fib->route_list_count
	);

	uint64_t *route_indexes = ADDR_OF(&fib->route_indexes);
	mem_array_free_exp(
		memory_context,
		route_indexes,
		sizeof(*route_indexes),
		fib->route_index_count
	);

	lpm_free(&fib->lpm_v6);
	lpm_free(&fib->lpm_v4);
}

int
route_fib_add_route(
	struct route_fib *fib,
	struct memory_context *memory_context,
	struct ether_addr dst_addr,
	struct ether_addr src_addr,
	uint64_t device_index,
	uint64_t counter_id
) {
	struct route *routes = ADDR_OF(&fib->routes);

	if (mem_array_expand_exp(
		    memory_context,
		    (void **)&routes,
		    sizeof(*routes),
		    &fib->route_count
	    )) {
		return -1;
	}

	routes[fib->route_count - 1] = (struct route){
		.dst_addr = dst_addr,
		.src_addr = src_addr,
		.device_id = device_index,
		.counter_id = counter_id,
	};
	SET_OFFSET_OF(&fib->routes, routes);

	return fib->route_count - 1;
}

int
route_fib_add_route_list(
	struct route_fib *fib,
	struct memory_context *memory_context,
	size_t count,
	const uint32_t *indexes
) {
	uint64_t start = fib->route_index_count;

	uint64_t *route_indexes = ADDR_OF(&fib->route_indexes);

	// Grows the index array one slot per nexthop. Lists hold a handful of
	// nexthops, so the per-slot growth has not been worth batching.
	for (size_t idx = 0; idx < count; ++idx) {
		if (mem_array_expand_exp(
			    memory_context,
			    (void **)&route_indexes,
			    sizeof(*route_indexes),
			    &fib->route_index_count
		    )) {
			return -1;
		}
		route_indexes[fib->route_index_count - 1] = indexes[idx];

		// The array may have moved, so the table must not be left
		// pointing at the old block even if a later step fails.
		SET_OFFSET_OF(&fib->route_indexes, route_indexes);
	}

	struct route_list *route_lists = ADDR_OF(&fib->route_lists);
	if (mem_array_expand_exp(
		    memory_context,
		    (void **)&route_lists,
		    sizeof(*route_lists),
		    &fib->route_list_count
	    )) {
		return -1;
	}
	route_lists[fib->route_list_count - 1] = (struct route_list){
		.start = start,
		.count = count,
	};
	SET_OFFSET_OF(&fib->route_lists, route_lists);

	return fib->route_list_count - 1;
}

int
route_fib_add_prefix_v4(
	struct route_fib *fib,
	const uint8_t *from,
	const uint8_t *to,
	uint32_t route_list_index
) {
	return lpm_insert(&fib->lpm_v4, 4, from, to, route_list_index);
}

int
route_fib_add_prefix_v6(
	struct route_fib *fib,
	const uint8_t *from,
	const uint8_t *to,
	uint32_t route_list_index
) {
	return lpm_insert(&fib->lpm_v6, 16, from, to, route_list_index);
}

// Counts the LPM ranges over the whole key space of the given tree.
static uint64_t
route_lpm_range_count(const struct lpm *lpm, uint8_t key_size) {
	uint8_t from[LPM_KEY_SIZE_MAX];
	uint8_t to[LPM_KEY_SIZE_MAX];
	memset(from, 0x00, key_size);
	memset(to, 0xff, key_size);

	struct lpm_iter it;
	lpm_iter_init(&it, lpm, key_size, from, to);

	uint64_t count = 0;
	while (lpm_iter_next(&it)) {
		++count;
	}

	return count;
}

uint64_t
route_fib_range_count_v4(const struct route_fib *fib) {
	return route_lpm_range_count(&fib->lpm_v4, 4);
}

uint64_t
route_fib_range_count_v6(const struct route_fib *fib) {
	return route_lpm_range_count(&fib->lpm_v6, 16);
}

uint64_t
route_fib_nexthop_count(const struct route_fib *fib, uint32_t route_list_id) {
	if (route_list_id >= fib->route_list_count) {
		return 0;
	}
	struct route_list *route_lists = ADDR_OF(&fib->route_lists);
	return route_lists[route_list_id].count;
}

const struct route *
route_fib_resolve_route(
	const struct route_fib *fib,
	uint32_t route_list_id,
	uint64_t nexthop_idx
) {
	if (route_list_id >= fib->route_list_count) {
		return NULL;
	}

	struct route_list *route_lists = ADDR_OF(&fib->route_lists);
	struct route_list *route_list = &route_lists[route_list_id];
	if (nexthop_idx >= route_list->count) {
		return NULL;
	}

	uint64_t *route_indexes = ADDR_OF(&fib->route_indexes);
	uint64_t route_idx = route_indexes[route_list->start + nexthop_idx];
	if (route_idx >= fib->route_count) {
		return NULL;
	}

	struct route *routes = ADDR_OF(&fib->routes);
	return &routes[route_idx];
}

void
route_fib_iter_init(struct route_fib_iter *it, const struct route_fib *fib) {
	memset(it, 0, sizeof(*it));
	it->fib = fib;
	it->phase = route_fib_iter_phase_start;
}

bool
route_fib_iter_next(struct route_fib_iter *it) {
	if (it->phase == route_fib_iter_phase_done) {
		return false;
	}

	if (it->phase == route_fib_iter_phase_start) {
		uint8_t from[4] = {0, 0, 0, 0};
		uint8_t to[4] = {0xff, 0xff, 0xff, 0xff};
		lpm_iter_init(&it->lpm_it, &it->fib->lpm_v4, 4, from, to);
		it->phase = route_fib_iter_phase_ipv4;
	}

	if (it->phase == route_fib_iter_phase_ipv4) {
		if (lpm_iter_next(&it->lpm_it)) {
			return true;
		}

		// IPv4 exhausted, start IPv6.
		uint8_t from[16];
		uint8_t to[16];
		memset(from, 0x00, 16);
		memset(to, 0xff, 16);
		lpm_iter_init(&it->lpm_it, &it->fib->lpm_v6, 16, from, to);
		it->phase = route_fib_iter_phase_ipv6;
	}

	if (it->phase == route_fib_iter_phase_ipv6) {
		if (lpm_iter_next(&it->lpm_it)) {
			return true;
		}

		it->phase = route_fib_iter_phase_done;
	}

	return false;
}

uint8_t
route_fib_iter_address_family(const struct route_fib_iter *it) {
	return it->phase;
}

const uint8_t *
route_fib_iter_prefix_from(const struct route_fib_iter *it) {
	return it->lpm_it.cur_from;
}

const uint8_t *
route_fib_iter_prefix_to(const struct route_fib_iter *it) {
	return it->lpm_it.cur_to;
}

uint32_t
route_fib_iter_route_list_id(const struct route_fib_iter *it) {
	return it->lpm_it.cur_value;
}
