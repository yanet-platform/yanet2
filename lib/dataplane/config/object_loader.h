#pragma once

struct dp_config;

/*
 * Load an object type by looking up "new_object_<name>" through the
 * main-program handle bin_hndl and appending it to dp_config->dp_objects.
 *
 * Objects take no per-packet action — their preparation runs as the
 * execution-context commit hook, never inside the loader — so unlike
 * dp_load_module there is no plugin registry indirection: the
 * constructor is resolved through the main-program handle only,
 * mirroring dp_load_device. That handle also sees the symbols of a
 * plugin loaded with global symbols, so a plugin's object type loads the
 * same way; the type keeps the commit hook and prepared size its
 * constructor declares.
 *
 * Returns 0 on success, -1 on dlsym miss or out-of-memory.
 */
int
dp_load_object(struct dp_config *dp_config, void *bin_hndl, const char *name);
