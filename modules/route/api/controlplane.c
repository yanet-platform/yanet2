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

int
route_module_config_register_counters(
	struct route_module_config *config, yanet_error **err
) {
	struct {
		const char *name;
		uint64_t size;
		uint64_t *dst;
	} counters[] = {
		{"route_forwarded_v4", 2, &config->counters_v4.forwarded},
		{"route_forwarded_v6", 2, &config->counters_v6.forwarded},
		{"route_drop_no_route_v4", 2, &config->counters_v4.drop_no_route
		},
		{"route_drop_no_route_v6", 2, &config->counters_v6.drop_no_route
		},
		{"route_drop_ttl_expired_v4",
		 2,
		 &config->counters_v4.drop_ttl_expired},
		{"route_drop_ttl_expired_v6",
		 2,
		 &config->counters_v6.drop_ttl_expired},
		{"route_drop_non_ip", 2, &config->drop_non_ip_counter_id},
		{"route_drop_empty_route_list_v4",
		 2,
		 &config->counters_v4.drop_empty_route_list},
		{"route_drop_empty_route_list_v6",
		 2,
		 &config->counters_v6.drop_empty_route_list},
		{"route_drop_device_unresolved_v4",
		 2,
		 &config->counters_v4.drop_device_unresolved},
		{"route_drop_device_unresolved_v6",
		 2,
		 &config->counters_v6.drop_device_unresolved},
	};

	for (size_t i = 0; i < sizeof(counters) / sizeof(counters[0]); ++i) {
		uint64_t id = counter_registry_register(
			&config->cp_module.counter_registry,
			counters[i].name,
			counters[i].size,
			err
		);
		if (id == (uint64_t)-1) {
			yanet_error_add(
				err,
				"failed to register counter '%s'",
				counters[i].name
			);
			return -1;
		}
		*counters[i].dst = id;
	}

	return 0;
}

static void
route_module_config_destroy(struct cp_module *cp_module);

struct cp_module *
route_module_config_new(
	struct agent *agent, const char *name, yanet_error **err
) {
	struct route_module_config *config =
		(struct route_module_config *)memory_balloc(
			&agent->memory_context,
			sizeof(struct route_module_config)
		);
	if (config == NULL) {
		yanet_error_add(err, "failed to allocate config");
		return NULL;
	}

	if (cp_module_init(
		    &config->cp_module,
		    agent,
		    "route",
		    name,
		    route_module_config_destroy,
		    err
	    )) {
		yanet_error_add(err, "failed to init module");
		memory_bfree(
			&agent->memory_context,
			config,
			sizeof(struct route_module_config)
		);
		return NULL;
	}

	if (route_module_config_data_init(
		    config, &config->cp_module.memory_context
	    )) {
		yanet_error_add(err, "failed to init config data");
		// Frees directly instead of going through the type destructor.
		//
		// A failed configuration-data setup never reaches a state its
		// own teardown could safely walk. No reference beyond the
		// caller's own has been taken, and no registry has observed
		// the module yet, so nothing is lost by freeing the block
		// here.
		cp_module_fini(&config->cp_module);
		memory_bfree(
			&agent->memory_context,
			config,
			sizeof(struct route_module_config)
		);
		return NULL;
	}

	if (route_module_config_register_counters(config, err)) {
		// Frees directly instead of going through the public free.
		//
		// Registering counters is the last construction step, so
		// configuration data is already fully set up and the type's
		// own destructor can safely walk it. No reference beyond the
		// caller's own has been taken and no registry has observed
		// the module yet, so going through the public free would
		// only park it instead of destroying it.
		route_module_config_destroy(&config->cp_module);
		return NULL;
	}

	return &config->cp_module;
}

int
route_module_config_data_init(
	struct route_module_config *config,
	struct memory_context *memory_context
) {
	if (lpm_init(&config->lpm_v4, memory_context, "lpm_v4")) {
		return -1;
	}
	if (lpm_init(&config->lpm_v6, memory_context, "lpm_v6")) {
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
route_module_config_data_fini(struct route_module_config *config) {
	struct route *routes = ADDR_OF(&config->routes);
	mem_array_free_exp(
		&config->cp_module.memory_context,
		routes,
		sizeof(*routes),
		config->route_count
	);

	struct route_list *route_lists = ADDR_OF(&config->route_lists);
	mem_array_free_exp(
		&config->cp_module.memory_context,
		route_lists,
		sizeof(*route_lists),
		config->route_list_count
	);

	uint64_t *route_indexes = ADDR_OF(&config->route_indexes);
	mem_array_free_exp(
		&config->cp_module.memory_context,
		route_indexes,
		sizeof(*route_indexes),
		config->route_index_count
	);

	lpm_free(&config->lpm_v6);
	lpm_free(&config->lpm_v4);
}

static void
route_module_config_destroy(struct cp_module *cp_module) {
	struct route_module_config *config =
		container_of(cp_module, struct route_module_config, cp_module);

	route_module_config_data_fini(config);

	struct agent *agent = ADDR_OF(&cp_module->agent);

	cp_module_fini(cp_module);

	memory_bfree(
		&agent->memory_context,
		config,
		sizeof(struct route_module_config)
	);
}

void
route_module_config_free(struct cp_module *cp_module) {
	cp_module_release(cp_module);
}

int
route_module_config_add_route(
	struct cp_module *cp_module,
	struct ether_addr dst_addr,
	struct ether_addr src_addr,
	const char *device_name,
	const char *counter_name,
	yanet_error **err
) {
	struct route_module_config *config =
		container_of(cp_module, struct route_module_config, cp_module);
	struct route *routes = ADDR_OF(&config->routes);

	uint64_t device_index;
	if (cp_module_link_device(cp_module, device_name, &device_index, err)) {
		return -1;
	}

	uint64_t counter_id = COUNTER_INVALID;
	if (counter_name != NULL && counter_name[0] != '\0') {
		counter_id = counter_registry_register(
			&config->cp_module.counter_registry,
			counter_name,
			2,
			err
		);
		if (counter_id == COUNTER_INVALID) {
			yanet_error_add(
				err,
				"failed to register counter '%s'",
				counter_name
			);
			return -1;
		}
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
		.counter_id = counter_id,
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
route_module_config_route_count(struct cp_module *cp_module) {
	struct route_module_config *config =
		container_of(cp_module, struct route_module_config, cp_module);
	return config->route_count;
}

uint64_t
route_module_config_fib_range_count_v4(struct cp_module *cp_module) {
	struct route_module_config *config =
		container_of(cp_module, struct route_module_config, cp_module);
	return route_lpm_range_count(&config->lpm_v4, 4);
}

uint64_t
route_module_config_fib_range_count_v6(struct cp_module *cp_module) {
	struct route_module_config *config =
		container_of(cp_module, struct route_module_config, cp_module);
	return route_lpm_range_count(&config->lpm_v6, 16);
}

struct fib_iter *
fib_iter_new(struct cp_module *cp_module) {
	struct fib_iter *it = calloc(1, sizeof(*it));
	if (it == NULL) {
		return NULL;
	}
	it->config =
		container_of(cp_module, struct route_module_config, cp_module);
	return it;
}

void
fib_iter_free(struct fib_iter *it) {
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
	if (rli >= config->route_list_count) {
		return 0;
	}
	struct route_list *rls = ADDR_OF(&config->route_lists);
	return rls[rli].count;
}

// Resolves the route for the i-th nexthop of the current entry.
static const struct route *
fib_iter_resolve_route(const struct fib_iter *it, uint64_t nexthop_idx) {
	uint32_t rli = it->lpm_it.cur_value;
	struct route_module_config *config = it->config;
	if (rli >= config->route_list_count) {
		return NULL;
	}

	struct route_list *rls = ADDR_OF(&config->route_lists);
	struct route_list *rl = &rls[rli];
	if (nexthop_idx >= rl->count) {
		return NULL;
	}

	uint64_t *route_indexes = ADDR_OF(&config->route_indexes);
	uint64_t route_idx = route_indexes[rl->start + nexthop_idx];
	if (route_idx >= config->route_count) {
		return NULL;
	}

	struct route *routes = ADDR_OF(&config->routes);
	return &routes[route_idx];
}

void
fib_iter_nexthop_dst_mac(
	const struct fib_iter *it, uint64_t nexthop_idx, struct ether_addr *dst
) {
	const struct route *r = fib_iter_resolve_route(it, nexthop_idx);
	if (r != NULL) {
		*dst = r->dst_addr;
	} else {
		memset(dst, 0, sizeof(*dst));
	}
}

void
fib_iter_nexthop_src_mac(
	const struct fib_iter *it, uint64_t nexthop_idx, struct ether_addr *dst
) {
	const struct route *r = fib_iter_resolve_route(it, nexthop_idx);
	if (r != NULL) {
		*dst = r->src_addr;
	} else {
		memset(dst, 0, sizeof(*dst));
	}
}

const char *
fib_iter_nexthop_device_name(const struct fib_iter *it, uint64_t nexthop_idx) {
	const struct route *r = fib_iter_resolve_route(it, nexthop_idx);
	if (r == NULL) {
		return "";
	}

	struct route_module_config *config = it->config;
	struct cp_module_device *devices = ADDR_OF(&config->cp_module.devices);
	if (r->device_id < config->cp_module.device_count) {
		return devices[r->device_id].name;
	}
	return "";
}

const char *
fib_iter_nexthop_counter_name(const struct fib_iter *it, uint64_t nexthop_idx) {
	const struct route *r = fib_iter_resolve_route(it, nexthop_idx);
	if (r == NULL || r->counter_id == COUNTER_INVALID) {
		return "";
	}

	struct route_module_config *config = it->config;
	struct counter_registry *registry = &config->cp_module.counter_registry;
	if (r->counter_id < registry->count) {
		return ADDR_OF(&registry->names)[r->counter_id].name;
	}
	return "";
}
