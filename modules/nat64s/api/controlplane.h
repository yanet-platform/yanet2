#pragma once

#include <stdint.h>

#include "lib/errors/errors.h"
#include "lib/fwstate/nat64state.h"

struct agent;
struct cp_module;
struct yanet_shm;

/* The whole replaceable state of one nat64s module config. An update
 * applies to a freshly created config; the map names link the session
 * tables (NULL or empty links none). */
struct nat64s_module_update {
	/* NAT64 prefix for v4->v6 destinations, NAT64S_PREFIX_LEN bytes */
	const uint8_t *prefix;
	/* Public IPv4 pool in network byte order */
	const uint32_t *pool;
	uint64_t pool_count;
	struct nat64state_timeouts timeouts;
	uint16_t mtu_ipv4;
	uint16_t mtu_ipv6;
	/* Instance discriminator in the session keys */
	uint32_t nat64s_id;
	/* Name of the linked nat64state_lan_map object */
	const char *lan_map_name;
	/* Name of the linked nat64state_wan_map object */
	const char *wan_map_name;
};

/**
 * Create a nat64s module configuration in the agent's shared memory.
 * The returned config is empty (no pool, no links) and must be filled by
 * nat64s_module_config_update before it is published.
 */
struct cp_module *
nat64s_module_config_create(
	struct agent *agent, const char *name, yanet_error **err
);

/**
 * Destroy a dangling nat64s module configuration and return its memory.
 * Returns -1 with errno EAGAIN while a live generation still references
 * the config.
 */
int
nat64s_module_config_free(struct cp_module *cp_module, yanet_error **err);

/**
 * Fill a freshly created nat64s module configuration: copy the prefix,
 * pool, timeouts and MTU limits, and link the session map objects.
 * Zero timeout fields select their defaults; the names link nothing when
 * NULL or empty.
 */
int
nat64s_module_config_update(
	struct cp_module *cp_module,
	const struct nat64s_module_update *update,
	yanet_error **err
);
