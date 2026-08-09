#pragma once

#include <stddef.h>
#include <stdint.h>

#include "lib/errors/errors.h"
#include "lib/fwstate/fwstate_cursor.h"

struct agent;
struct cp_module;
struct fwstate_sync_config;

struct cp_module *
fwstate_module_config_new(
	struct agent *agent, const char *name, yanet_error **err
);

void
fwstate_module_config_free(struct cp_module *cp_module);

// Configure a fwstate sync config and link the named fwstate-map objects.
//
// fw4_name and fw6_name are the names of standalone fwstate_map_v4 /
// fwstate_map_v6 objects the module borrows its fwtables from. Either may
// be NULL or empty, in which case no link is declared and the dataplane
// resolves a NULL fwtable for that family. Returns 0 on success or -1 on
// error.
int
fwstate_module_config_set(
	struct cp_module *cp_module,
	const char *fw4_name,
	const char *fw6_name,
	const struct fwstate_sync_config *sync_config,
	yanet_error **err
);

struct fwstate_sync_config
fwstate_config_get_sync_config(const struct cp_module *cp_module);
