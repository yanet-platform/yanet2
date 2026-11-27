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

#include "objects/fwstate/api/fwstate_map_nat64_object.h"

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

	uint8_t *prefixes = ADDR_OF(&config->prefixes.list);
	if (prefixes != NULL) {
		memory_bfree(
			memory_context,
			prefixes,
			NAT64S_PREFIX_LEN * config->prefixes.count
		);
	}

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

	// balloc does not initialize: zero the enclosing struct so the
	// stats array starts from zero, not from shared-memory garbage.
	memset(config, 0, sizeof(*config));

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
	config->prefixes.list = NULL;
	config->prefixes.count = 0;
	config->map_object_link_idx = NAT64S_OBJECT_LINK_NONE;

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

	// A repeated update replaces the arrays whole: free what a prior
	// call allocated so the replacement cannot leak.
	uint8_t *old_prefixes = ADDR_OF(&config->prefixes.list);
	if (old_prefixes != NULL) {
		memory_bfree(
			memory_context,
			old_prefixes,
			NAT64S_PREFIX_LEN * config->prefixes.count
		);
	}
	uint32_t *old_pool = ADDR_OF(&config->pool.pool);
	if (old_pool != NULL) {
		memory_bfree(
			memory_context,
			old_pool,
			sizeof(uint32_t) * config->pool.count
		);
	}
	config->prefixes.list = NULL;
	config->prefixes.count = 0;
	config->pool.pool = NULL;
	config->pool.count = 0;

	if (update->prefix_count > 0 && update->prefixes != NULL) {
		uint8_t *prefixes = memory_balloc(
			memory_context, NAT64S_PREFIX_LEN * update->prefix_count
		);
		if (prefixes == NULL) {
			yanet_error_add(err, "failed to allocate the prefixes");
			return -1;
		}
		memcpy(prefixes,
		       update->prefixes,
		       NAT64S_PREFIX_LEN * update->prefix_count);
		SET_OFFSET_OF(&config->prefixes.list, prefixes);
		config->prefixes.count = update->prefix_count;
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

	// The bounds are held to what a 16-bit port field carries, the upper
	// bound excluded.
	uint32_t port_lo = update->port_lo;
	uint32_t port_hi = update->port_hi;
	if (port_lo >= NAT64S_PORT_HI_MAX) {
		port_lo = 1024;
	}
	if (port_hi > NAT64S_PORT_HI_MAX) {
		port_hi = NAT64S_PORT_HI_MAX;
	}

	// A window with hi <= lo or the zeroes selects the default range.
	config->port_lo = (port_hi > port_lo) ? port_lo : 1024;
	config->port_hi = (port_hi > port_lo) ? port_hi : NAT64S_PORT_HI_MAX;

	// The timeouts arrive narrowed to their knob already, the largest
	// value it represents included. Zero stays zero: the dataplane
	// resolves each knob's zero to that knob's own default.
	config->timeouts.timeout = update->timeouts.timeout;
	config->timeouts.tcp_established = update->timeouts.tcp_established;

	// Zero selects the defaults; the translation core's length
	// arithmetic assumes a nonzero limit, and holds the IPv6 difference
	// in a signed 16-bit number.
	config->mtu.ipv4 = update->mtu_ipv4 ? update->mtu_ipv4 : 1450;
	config->mtu.ipv6 = update->mtu_ipv6
				   ? (update->mtu_ipv6 < NAT64S_MTU_IPV6_MAX
					      ? update->mtu_ipv6
					      : NAT64S_MTU_IPV6_MAX)
				   : 1280;

	// Link the session map objects; the module is freshly constructed, so
	// no earlier links exist.
	config->map_object_link_idx = NAT64S_OBJECT_LINK_NONE;

	if (update->map_name != NULL && update->map_name[0] != '\0') {
		if (cp_module_link_object(
			    cp_module,
			    FWSTATE_MAP_NAT64_OBJECT_TYPE,
			    update->map_name,
			    &config->map_object_link_idx,
			    err
		    )) {
			return -1;
		}
	}

	return 0;
}
