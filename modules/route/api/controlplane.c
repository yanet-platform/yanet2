#include "controlplane.h"

#include "config.h"

#include "common/container_of.h"
#include "common/exp_array.h"
#include "common/lpm.h"

#include "controlplane/agent/agent.h"

enum fib_iter_phase {
	fib_iter_phase_start = 0,
	fib_iter_phase_ipv4 = 4,
	fib_iter_phase_ipv6 = 6,
	fib_iter_phase_done = 0xff,
};

struct fib_iter {
	struct route_module_config *config;
	struct lpm_iter lpm_it;
	enum fib_iter_phase phase;
};

struct cp_module *
route_module_config_create(struct agent *agent, const char *name) {
	struct route_module_config *config =
		(struct route_module_config *)memory_balloc(
			&agent->memory_context,
			sizeof(struct route_module_config)
		);
	if (config == NULL) {
		errno = ENOMEM;
		return NULL;
	}

	if (cp_module_init(&config->cp_module, agent, "route", name)) {
		memory_bfree(
			&agent->memory_context,
			config,
			sizeof(struct route_module_config)
		);
	}

	if (route_module_config_data_init(
		    config, &config->cp_module.memory_context
	    )) {
		memory_bfree(
			&agent->memory_context,
			config,
			sizeof(struct route_module_config)
		);
		return NULL;
	}

	return &config->cp_module;
}

void
route_module_config_free(struct cp_module *cp_module) {
	struct route_module_config *config =
		container_of(cp_module, struct route_module_config, cp_module);

	struct agent *agent = ADDR_OF(&cp_module->agent);
	memory_bfree(
		&agent->memory_context,
		config,
		sizeof(struct route_module_config)
	);
};

int
route_module_config_data_init(
	struct route_module_config *config,
	struct memory_context *memory_context
) {
	if (lpm_init(&config->lpm_v4, memory_context))
		return -1;
	if (lpm_init(&config->lpm_v6, memory_context)) {
		lpm_free(&config->lpm_v4);
		return -1;
	}

	config->route_count = 0;
	config->routes = NULL;

	config->route_list_count = 0;
	config->route_lists = NULL;

	config->route_index_count = 0;
	config->route_indexes = NULL;

	return 0;
}

void
route_module_config_data_destroy(struct route_module_config *config) {
	struct route *routes = ADDR_OF(&config->routes);
	memory_bfree(
		&config->cp_module.memory_context,
		routes,
		sizeof(struct route) * config->route_count
	);

	struct route_list *route_lists = ADDR_OF(&config->route_lists);
	memory_bfree(
		&config->cp_module.memory_context,
		route_lists,
		sizeof(struct route_list) * config->route_list_count
	);

	uint64_t *route_indexes = ADDR_OF(&config->route_indexes);
	memory_bfree(
		&config->cp_module.memory_context,
		route_indexes,
		sizeof(uint64_t) * config->route_index_count
	);

	lpm_free(&config->lpm_v6);
	lpm_free(&config->lpm_v4);
}

int
route_module_config_add_route(
	struct cp_module *cp_module,
	struct ether_addr dst_addr,
	struct ether_addr src_addr,
	const char *device_name
) {
	struct route_module_config *config =
		container_of(cp_module, struct route_module_config, cp_module);
	struct route *routes = ADDR_OF(&config->routes);

	uint64_t device_index;
	if (cp_module_link_device(cp_module, device_name, &device_index)) {
		return -1;
	}

	if (mem_array_expand_exp(
		    &config->cp_module.memory_context,
		    (void **)&routes,
		    sizeof(*routes),
		    &config->route_count
	    )) {
		return -1;
	}

	routes[config->route_count - 1] = (struct route){
		.dst_addr = dst_addr,
		.src_addr = src_addr,
		.device_id = device_index,
	};
	SET_OFFSET_OF(&config->routes, routes);

	return config->route_count - 1;
}

int
route_module_config_add_route_list(
	struct cp_module *cp_module, size_t count, const uint32_t *indexes
) {
	struct route_module_config *config =
		container_of(cp_module, struct route_module_config, cp_module);

	uint64_t start = config->route_index_count;

	uint64_t *route_indexes = ADDR_OF(&config->route_indexes);

	for (size_t idx = 0; idx < count; ++idx) {
		/*
		 * FIXME: if there are huge loads of route indexes then
		 * the loop may be inefficient. However, I do not expect
		 * more than 10 route indexes typically - so I let it
		 * out of scope now.
		 */
		if (mem_array_expand_exp(
			    &config->cp_module.memory_context,
			    (void **)&route_indexes,
			    sizeof(*route_indexes),
			    &config->route_index_count
		    )) {
			return -1;
		}
		route_indexes[config->route_index_count - 1] = indexes[idx];

		/*
		 * route_indexes may be relocated so save the new value
		 * as I do no want to have the config be completelly
		 * broken.
		 */
		SET_OFFSET_OF(&config->route_indexes, route_indexes);
	}

	struct route_list *route_lists = ADDR_OF(&config->route_lists);
	if (mem_array_expand_exp(
		    &config->cp_module.memory_context,
		    (void **)&route_lists,
		    sizeof(*route_lists),
		    &config->route_list_count
	    )) {
		return -1;
	}
	route_lists[config->route_list_count - 1] = (struct route_list){
		.start = start,
		.count = count,
	};

	SET_OFFSET_OF(&config->route_lists, route_lists);

	return config->route_list_count - 1;
}

int
route_module_config_add_prefix_v4(
	struct cp_module *cp_module,
	const uint8_t *from,
	const uint8_t *to,
	uint32_t route_list_index
) {
	struct route_module_config *config =
		container_of(cp_module, struct route_module_config, cp_module);
	return lpm_insert(&config->lpm_v4, 4, from, to, route_list_index);
}

int
route_module_config_add_prefix_v6(
	struct cp_module *cp_module,
	const uint8_t *from,
	const uint8_t *to,
	uint32_t route_list_index
) {
	struct route_module_config *config =
		container_of(cp_module, struct route_module_config, cp_module);
	return lpm_insert(&config->lpm_v6, 16, from, to, route_list_index);
}

struct fib_iter *
fib_iter_create(struct cp_module *cp_module) {
	struct fib_iter *it = calloc(1, sizeof(*it));
	if (it == NULL)
		return NULL;
	it->config =
		container_of(cp_module, struct route_module_config, cp_module);
	return it;
}

void
fib_iter_destroy(struct fib_iter *it) {
	free(it);
}

bool
fib_iter_next(struct fib_iter *it) {
	if (it->phase == fib_iter_phase_done) {
		return false;
	}

	// Start or continue IPv4 walk.
	if (it->phase == fib_iter_phase_start) {
		uint8_t from[4] = {0, 0, 0, 0};
		uint8_t to[4] = {0xff, 0xff, 0xff, 0xff};
		lpm_iter_init(&it->lpm_it, &it->config->lpm_v4, 4, from, to);
		it->phase = fib_iter_phase_ipv4;
	}

	if (it->phase == fib_iter_phase_ipv4) {
		if (lpm_iter_next(&it->lpm_it)) {
			return true;
		}

		// IPv4 exhausted, start IPv6.
		uint8_t from[16];
		uint8_t to[16];
		memset(from, 0x00, 16);
		memset(to, 0xff, 16);
		lpm_iter_init(&it->lpm_it, &it->config->lpm_v6, 16, from, to);
		it->phase = fib_iter_phase_ipv6;
	}

	if (it->phase == fib_iter_phase_ipv6) {
		if (lpm_iter_next(&it->lpm_it)) {
			return true;
		}

		it->phase = fib_iter_phase_done;
	}

	return false;
}

uint8_t
fib_iter_address_family(const struct fib_iter *it) {
	return it->phase;
}

const uint8_t *
fib_iter_prefix_from(const struct fib_iter *it) {
	return it->lpm_it.cur_from;
}

const uint8_t *
fib_iter_prefix_to(const struct fib_iter *it) {
	return it->lpm_it.cur_to;
}

uint64_t
fib_iter_nexthop_count(const struct fib_iter *it) {
	uint32_t rli = it->lpm_it.cur_value;
	struct route_module_config *config = it->config;
	if (rli >= config->route_list_count)
		return 0;
	struct route_list *rls = ADDR_OF(&config->route_lists);
	return rls[rli].count;
}

// Resolves the route for the i-th nexthop of the current entry.
static const struct route *
fib_iter_resolve_route(const struct fib_iter *it, uint64_t nexthop_idx) {
	uint32_t rli = it->lpm_it.cur_value;
	struct route_module_config *config = it->config;
	if (rli >= config->route_list_count)
		return NULL;

	struct route_list *rls = ADDR_OF(&config->route_lists);
	struct route_list *rl = &rls[rli];
	if (nexthop_idx >= rl->count)
		return NULL;

	uint64_t *route_indexes = ADDR_OF(&config->route_indexes);
	uint64_t route_idx = route_indexes[rl->start + nexthop_idx];
	if (route_idx >= config->route_count)
		return NULL;

	struct route *routes = ADDR_OF(&config->routes);
	return &routes[route_idx];
}

void
fib_iter_nexthop_dst_mac(
	const struct fib_iter *it, uint64_t nexthop_idx, struct ether_addr *dst
) {
	const struct route *r = fib_iter_resolve_route(it, nexthop_idx);
	if (r != NULL)
		*dst = r->dst_addr;
	else
		memset(dst, 0, sizeof(*dst));
}

void
fib_iter_nexthop_src_mac(
	const struct fib_iter *it, uint64_t nexthop_idx, struct ether_addr *dst
) {
	const struct route *r = fib_iter_resolve_route(it, nexthop_idx);
	if (r != NULL)
		*dst = r->src_addr;
	else
		memset(dst, 0, sizeof(*dst));
}

const char *
fib_iter_nexthop_device_name(const struct fib_iter *it, uint64_t nexthop_idx) {
	const struct route *r = fib_iter_resolve_route(it, nexthop_idx);
	if (r == NULL)
		return "";

	struct route_module_config *config = it->config;
	struct cp_module_device *devices = ADDR_OF(&config->cp_module.devices);
	if (r->device_id < config->cp_module.device_count)
		return devices[r->device_id].name;
	return "";
}
