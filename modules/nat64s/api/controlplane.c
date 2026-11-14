/* Control-plane API of the stateful NAT64 module: create, fill and
 * destroy module configurations in the agent's shared memory. */

/* System headers */
#include <errno.h>
#include <string.h>

/* Project headers */
#include "controlplane.h"
#include "lib/controlplane/config/cp_module.h"

/* Common headers */
#include "common/container_of.h"
#include "common/memory_address.h"
#include "lib/errors/errors.h"
#include "lib/logging/log.h"

#include "lib/controlplane/agent/agent.h"

#include "objects/fwstate/api/fwstate_map_nat64_lan_object.h"
#include "objects/fwstate/api/fwstate_map_nat64_wan_object.h"

#include "modules/nat64s/dataplane/config.h"

// Typed destructor: frees the pool array and the module block. Runs only
// on a dangling config, after nat64s_module_config_free wins the
// try-destroy race.
static void
nat64s_module_config_destroy(struct cp_module *cp_module) {
	struct nat64s_module_config *config =
		container_of(cp_module, struct nat64s_module_config, cp_module);
	struct memory_context *memory_context =
		&cp_module->entity.memory_context;

	uint32_t *pool = ADDR_OF(&config->pool.pool);
	if (pool != NULL) {
		memory_bfree(
			memory_context,
			pool,
			sizeof(uint32_t) * config->pool.count
		);
	}

	// Capture agent before fini zeroes it.
	struct agent *agent = ADDR_OF(&cp_module->entity.agent);

	cp_module_fini(cp_module);

	memory_bfree(
		&agent->memory_context,
		config,
		sizeof(struct nat64s_module_config)
	);
}

struct cp_module *
nat64s_module_config_create(
	struct agent *agent, const char *name, yanet_error **err
) {
	struct nat64s_module_config *config =
		(struct nat64s_module_config *)memory_balloc(
			&agent->memory_context,
			sizeof(struct nat64s_module_config)
		);
	if (config == NULL) {
		yanet_error_add(err, "failed to allocate config");
		return NULL;
	}

	if (cp_module_init(&config->cp_module, agent, "nat64s", name, err)) {
		yanet_error_add(err, "failed to init module");
		memory_bfree(
			&agent->memory_context,
			config,
			sizeof(struct nat64s_module_config)
		);
		return NULL;
	}

	config->pool.pool = NULL;
	config->pool.count = 0;
	config->lan_object_link_idx = NAT64S_OBJECT_LINK_NONE;
	config->wan_object_link_idx = NAT64S_OBJECT_LINK_NONE;

	return &config->cp_module;
}

int
nat64s_module_config_free(struct cp_module *cp_module, yanet_error **err) {
	if (cp_module_try_destroy(cp_module, err)) {
		return -1;
	}

	struct agent *owner = ADDR_OF(&cp_module->entity.agent);

	nat64s_module_config_destroy(cp_module);
	agent_destroy_released(owner);
	return 0;
}

// Clamp one timeout knob into (0, NAT64STATE_TIMEOUT_MAX], zero selecting
// the default.
static uint16_t
nat64s_timeout_clamp(uint16_t value) {
	if (value == 0) {
		return NAT64STATE_TIMEOUT_DEFAULT;
	}
	if (value >= NAT64STATE_TIMEOUT_MAX) {
		return NAT64STATE_TIMEOUT_MAX - 1;
	}
	return value;
}

int
nat64s_module_config_update(
	struct cp_module *cp_module,
	const struct nat64s_module_update *update,
	yanet_error **err
) {
	struct nat64s_module_config *config =
		container_of(cp_module, struct nat64s_module_config, cp_module);
	struct memory_context *memory_context =
		&cp_module->entity.memory_context;

	if (update->prefix != NULL) {
		memcpy(config->prefix, update->prefix, NAT64S_PREFIX_LEN);
	}

	if (update->pool_count > 0 && update->pool != NULL) {
		uint32_t *pool = memory_balloc(
			memory_context, sizeof(uint32_t) * update->pool_count
		);
		if (pool == NULL) {
			yanet_error_add(err, "failed to allocate the pool");
			return -1;
		}
		memcpy(pool, update->pool, sizeof(uint32_t) * update->pool_count
		);
		SET_OFFSET_OF(&config->pool.pool, pool);
		config->pool.count = update->pool_count;
	}

	config->timeouts = update->timeouts;
	config->timeouts.tcp_syn =
		nat64s_timeout_clamp(config->timeouts.tcp_syn);
	config->timeouts.tcp_ack =
		nat64s_timeout_clamp(config->timeouts.tcp_ack);
	config->timeouts.tcp_fin =
		nat64s_timeout_clamp(config->timeouts.tcp_fin);
	config->timeouts.udp = nat64s_timeout_clamp(config->timeouts.udp);
	config->timeouts.icmp = nat64s_timeout_clamp(config->timeouts.icmp);
	config->timeouts.other = nat64s_timeout_clamp(config->timeouts.other);

	config->mtu.ipv4 = update->mtu_ipv4;
	config->mtu.ipv6 = update->mtu_ipv6;
	config->nat64s_id = update->nat64s_id;

	// Link the session map objects; the module is freshly constructed, so
	// no earlier links exist.
	config->lan_object_link_idx = NAT64S_OBJECT_LINK_NONE;
	config->wan_object_link_idx = NAT64S_OBJECT_LINK_NONE;

	if (update->lan_map_name != NULL && update->lan_map_name[0] != '\0') {
		if (cp_module_link_object(
			    cp_module,
			    FWSTATE_MAP_NAT64_LAN_OBJECT_TYPE,
			    update->lan_map_name,
			    &config->lan_object_link_idx,
			    err
		    )) {
			return -1;
		}
	}
	if (update->wan_map_name != NULL && update->wan_map_name[0] != '\0') {
		if (cp_module_link_object(
			    cp_module,
			    FWSTATE_MAP_NAT64_WAN_OBJECT_TYPE,
			    update->wan_map_name,
			    &config->wan_object_link_idx,
			    err
		    )) {
			return -1;
		}
	}

	return 0;
}
