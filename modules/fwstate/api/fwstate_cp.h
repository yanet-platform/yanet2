#pragma once

#include "common/network.h"

#include "lib/errors/errors.h"
#include "modules/fwstate/dataplane/config.h"

#include <stdint.h>

struct agent;
struct cp_module;

// Clear all synchronization fields, then install the production timeouts.
void
fwstate_config_set_defaults(struct fwstate_sync_config *config);

// Allocate an fwstate module config fully built in one step: a config
// handle is created once and never updated afterwards.
//
// sync_config carries the scalar settings - timeouts, suppression
// window, sync MTU - or NULL to keep the defaults; its array members
// are ignored. The endpoints and listeners arrive as separately
// sized arrays, copied into the config's memory tree with no fixed
// bound; either may be NULL with a zero count.
//
// fw4_map_name and fw6_map_name name standalone fwstate_map_v4 /
// fwstate_map_v6 objects whose fwtables the module inserts synced state
// into. Either may be NULL or empty, in which case no link is declared
// and the module counts and drops that family's sync frames without
// inserting. The names are validated when the module is installed into a
// configuration generation; an unknown name fails that installation and
// the previous configuration stays live.
//
// Returns NULL with err set on failure; nothing is left allocated.
struct cp_module *
fwstate_module_config_new(
	struct agent *agent,
	const char *name,
	const struct fwstate_sync_config *sync_config,
	const struct fwstate_sync_endpoint *endpoints,
	uint32_t endpoint_count,
	const struct fwstate_sync_listen_endpoint *listeners,
	uint32_t listener_count,
	const char *fw4_map_name,
	const char *fw6_map_name,
	yanet_error **err
);

// Destroy the module when it is dangling, per cp_module_try_destroy.
//
// Returns -1 with errno EAGAIN while a live generation still references
// the module; the caller must keep its handle and retry later.
int
fwstate_module_config_free(struct cp_module *cp_module, yanet_error **err);
