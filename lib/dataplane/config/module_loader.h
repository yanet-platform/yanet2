#pragma once

struct dp_config;

// Load a packet-processing module via dlsym(bin_hndl, "new_module_<name>")
// and append it to dp_config->dp_modules. Returns 0 on success, -1 on
// dlsym miss or out-of-memory.
int
dp_load_module(struct dp_config *dp_config, void *bin_hndl, const char *name);

// Load a device adapter via dlsym(bin_hndl, "new_device_<name>") and append
// it to dp_config->dp_devices, copying both input and output handlers.
// Returns 0 on success, -1 on dlsym miss or out-of-memory.
int
dp_load_device(struct dp_config *dp_config, void *bin_hndl, const char *name);
