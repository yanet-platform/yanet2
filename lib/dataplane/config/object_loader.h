#pragma once

struct dp_config;
#include <stddef.h>

/*
 * Load an object type through its exported "new_object_<name>" constructor
 * and register it with the dataplane config.
 *
 * The constructor resolves from the main program, which also sees every
 * plugin loaded with global symbols, so a built-in and a plugin-provided
 * type load the same way, keeping the commit hook and prepared size the
 * constructor declares. Returns 0 on success, -1 when the constructor is
 * missing, returns a type other than the requested one, or memory runs out.
 */
int
dp_load_object(struct dp_config *dp_config, void *bin_hndl, const char *name);

/*
 * Load each named object type, skipping a name whose type is already
 * registered, so a list may repeat a name or a built-in.
 *
 * Returns 0 when every name is registered afterwards, -1 on the first
 * name that fails to load.
 */
int
dp_load_objects(
	struct dp_config *dp_config,
	void *bin_hndl,
	const char *const *names,
	size_t count
);
