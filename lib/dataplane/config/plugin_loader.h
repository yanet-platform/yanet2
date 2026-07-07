#pragma once

#include <stdint.h>

#define PLUGIN_NAME_LEN 80

struct plugin_handle {
	char name[PLUGIN_NAME_LEN];
	void *dl_handle;
};

struct plugin_registry {
	struct plugin_handle *plugins;
	uint64_t count;
};

// Scan plugin_dir for lib*_dp.so files and dlopen each one.
// Returns 0 on success, -1 on fatal error.
//
// An empty plugin_dir is a clean no-op. Any other failure is fatal and
// returns -1 with the registry left empty: a plugin_dir that cannot be
// opened, a dlopen failure, an out-of-memory condition, a plugin missing
// the ABI version symbol, or an exported ABI version that does not match
// YANET_MODULE_ABI_VERSION. A present-but-broken plugin aborts startup
// rather than silently running without the configured module.
int
dp_load_plugins(const char *plugin_dir, struct plugin_registry *registry);

// dlclose all plugin handles and free the registry.
void
dp_unload_plugins(struct plugin_registry *registry);
