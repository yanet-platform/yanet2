#pragma once

struct dp_config;

/*
 * Load an object type by looking up "new_object_<name>" in the main
 * binary via bin_hndl and appending it to dp_config->dp_objects.
 *
 * Objects are inert, so unlike dp_load_module there is no plugin
 * registry indirection: the constructor is always resolved from the main
 * binary, mirroring dp_load_device.
 *
 * Returns 0 on success, -1 on dlsym miss or out-of-memory.
 */
int
dp_load_object(struct dp_config *dp_config, void *bin_hndl, const char *name);
