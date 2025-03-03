#include "controlplane.h"

#include "config.h"

#include "common/container_of.h"

#include "common/exp_array.h"

#include "controlplane/agent/agent.h"
#include "dataplane/config/zone.h"

struct module_data *
route_module_config_init(struct agent *agent, const char *name) {
	uint64_t index;
	if (dp_config_lookup_module(agent->dp_config, "route", &index)) {
		return NULL;
	}

	struct route_module_config *config =
		(struct route_module_config *)memory_balloc(
			&agent->memory_context,
			sizeof(struct route_module_config)
		);
	if (config == NULL)
		return NULL;

	config->module_data.index = index;
	strncpy(config->module_data.name,
		name,
		sizeof(config->module_data.name) - 1);
	memory_context_init_from(
		&config->module_data.memory_context,
		&agent->memory_context,
		name
	);

	// From the point all allocations are made on local memory context
	struct memory_context *memory_context =
		&config->module_data.memory_context;
	lpm_init(&config->lpm_v4, memory_context);
	lpm_init(&config->lpm_v6, memory_context);

	config->route_count = 0;
	config->routes = OFFSET_OF(config, NULL);

	config->route_list_count = 0;
	config->route_lists = OFFSET_OF(config, NULL);

	config->route_index_count = 0;
	config->route_indexes = OFFSET_OF(config, NULL);

	return &config->module_data;
}

int
route_module_config_add_route(
	struct module_data *module_data,
	struct ether_addr dst_addr,
	struct ether_addr src_addr
) {
	struct route_module_config *config = container_of(
		module_data, struct route_module_config, module_data
	);
	struct route *routes = ADDR_OF(config, config->routes);

	if (mem_array_expand_exp(
		    &config->module_data.memory_context,
		    (void **)&routes,
		    sizeof(*routes),
		    &config->route_count
	    )) {
		return -1;
	}

	routes[config->route_count - 1] = (struct route){
		.dst_addr = dst_addr,
		.src_addr = src_addr,
	};
	config->routes = OFFSET_OF(config, routes);

	return config->route_count - 1;
}

int
route_module_config_add_route_list(
	struct module_data *module_data, size_t count, const uint32_t *indexes
) {
	struct route_module_config *config = container_of(
		module_data, struct route_module_config, module_data
	);

	uint64_t start = config->route_index_count;

	uint64_t *route_indexes = ADDR_OF(config, config->route_indexes);

	for (size_t idx = 0; idx < count; ++idx) {
		/*
		 * FIXME: if there are huge loads of route indexes then
		 * the loop may be inefficient. However, I do not expect
		 * more than 10 route indexes typically - so I let it
		 * out of scope now.
		 */
		if (mem_array_expand_exp(
			    &config->module_data.memory_context,
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
		config->route_indexes = OFFSET_OF(config, route_indexes);
	}

	struct route_list *route_lists = ADDR_OF(config, config->route_lists);
	if (mem_array_expand_exp(
		    &config->module_data.memory_context,
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

	config->route_lists = OFFSET_OF(config, route_lists);

	return config->route_list_count - 1;
}

int
route_module_config_add_prefix_v4(
	struct module_data *module_data,
	const uint8_t *from,
	const uint8_t *to,
	uint32_t route_list_index
) {
	struct route_module_config *config = container_of(
		module_data, struct route_module_config, module_data
	);
	return lpm_insert(&config->lpm_v4, 4, from, to, route_list_index);
}

int
route_module_config_add_prefix_v6(
	struct module_data *module_data,
	const uint8_t *from,
	const uint8_t *to,
	uint32_t route_list_index
) {
	struct route_module_config *config = container_of(
		module_data, struct route_module_config, module_data
	);
	return lpm_insert(&config->lpm_v6, 16, from, to, route_list_index);
}
