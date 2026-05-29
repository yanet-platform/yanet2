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
// Individual plugin failures are logged but do not prevent other
// plugins from loading.
int
dp_load_plugins(const char *plugin_dir, struct plugin_registry *registry);

// dlclose all plugin handles and free the registry.
void
dp_unload_plugins(struct plugin_registry *registry);
