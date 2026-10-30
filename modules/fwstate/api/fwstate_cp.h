#pragma once

#include "common/filter_views.h"
#include "common/network.h"

#include "lib/classify/compiler/u16_ranges.h"
#include "lib/errors/errors.h"

#include <stdint.h>

struct agent;
struct cp_module;
struct fwstate_sync_config;

// Bounds of one synchronization scope rule and of the rule list. The
// control plane refuses larger lists before the config is built.
#define FWSTATE_SYNC_RULE_NETS_MAX 16
#define FWSTATE_SYNC_RULE_PORTS_MAX 8
#define FWSTATE_SYNC_RULES_MAX 64

// One synchronization scope rule in the flat form the control plane
// FFI carries it: the flow's source networks of both families and its
// source port ranges. A rule naming none of them matches every flow.
struct fwstate_sync_rule_input {
	struct net4 net4s[FWSTATE_SYNC_RULE_NETS_MAX];
	uint32_t net4_count;
	struct net6 net6s[FWSTATE_SYNC_RULE_NETS_MAX];
	uint32_t net6_count;
	struct filter_u16_span src_ports[FWSTATE_SYNC_RULE_PORTS_MAX];
	uint32_t src_port_count;
};

// Clear all synchronization fields, then install the production timeouts.
void
fwstate_config_set_defaults(struct fwstate_sync_config *config);

// Allocate an fwstate module config fully built in one step: a config
// handle is created once and never updated afterwards.
//
// sync_config is the sync config to install, or NULL to
// keep the defaults.
//
// rules is the synchronization scope - the flows whose state the module
// syncs - in the flat input form above, or NULL with rule_count zero
// for no scope: an unscoped module syncs everything.
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
	const struct fwstate_sync_rule_input *rules,
	uint32_t rule_count,
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

struct fwstate_sync_config
fwstate_config_get_sync_config(const struct cp_module *cp_module);
