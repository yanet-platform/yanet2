#include "plugin_loader.h"

#include <dirent.h>
#include <dlfcn.h>
#include <errno.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/stat.h>

#include "common/strutils.h"
#include "lib/logging/log.h"

#define PLUGIN_SO_PREFIX "lib"
#define PLUGIN_SO_SUFFIX "_dp.so"

// Extract module name from filename: "libnat44_dp.so" -> "nat44".
// Returns 0 on success, -1 if the filename does not match.
static int
plugin_name_from_filename(const char *filename, char *name, size_t name_len) {
	size_t prefix_len = strlen(PLUGIN_SO_PREFIX);
	size_t suffix_len = strlen(PLUGIN_SO_SUFFIX);
	size_t fn_len = strlen(filename);

	if (fn_len <= prefix_len + suffix_len)
		return -1;
	if (strncmp(filename, PLUGIN_SO_PREFIX, prefix_len) != 0)
		return -1;
	if (strcmp(filename + fn_len - suffix_len, PLUGIN_SO_SUFFIX) != 0)
		return -1;

	size_t mod_len = fn_len - prefix_len - suffix_len;
	if (mod_len >= name_len)
		return -1;

	memcpy(name, filename + prefix_len, mod_len);
	name[mod_len] = '\0';
	return 0;
}

int
dp_load_plugins(const char *plugin_dir, struct plugin_registry *registry) {
	registry->plugins = NULL;
	registry->count = 0;

	if (plugin_dir == NULL || plugin_dir[0] == '\0')
		return 0;

	DIR *dir = opendir(plugin_dir);
	if (dir == NULL) {
		LOG(WARN,
		    "cannot open plugin directory %s: %s",
		    plugin_dir,
		    strerror(errno));
		return 0;
	}

	struct dirent *entry;
	while ((entry = readdir(dir)) != NULL) {
		char name[PLUGIN_NAME_LEN];
		if (plugin_name_from_filename(
			    entry->d_name, name, sizeof(name)
		    ) != 0)
			continue;

		char so_path[512];
		snprintf(
			so_path,
			sizeof(so_path),
			"%s/%s",
			plugin_dir,
			entry->d_name
		);

		struct stat st;
		if (stat(so_path, &st) != 0 || !S_ISREG(st.st_mode))
			continue;

		void *dl = dlopen(so_path, RTLD_NOW | RTLD_GLOBAL);
		if (dl == NULL) {
			LOG(ERROR, "dlopen(%s) failed: %s", so_path, dlerror());
			continue;
		}

		struct plugin_handle *new_plugins =
			realloc(registry->plugins,
				sizeof(struct plugin_handle) *
					(registry->count + 1));
		if (new_plugins == NULL) {
			LOG(ERROR, "out of memory for plugin %s", name);
			dlclose(dl);
			continue;
		}
		registry->plugins = new_plugins;

		struct plugin_handle *handle =
			&registry->plugins[registry->count];
		strtcpy(handle->name, name, sizeof(handle->name));
		handle->dl_handle = dl;
		registry->count++;

		LOG(INFO, "loaded plugin %s from %s", name, so_path);
	}

	closedir(dir);

	LOG(INFO,
	    "loaded %lu external plugin(s) from %s",
	    (unsigned long)registry->count,
	    plugin_dir);
	return 0;
}

void
dp_unload_plugins(struct plugin_registry *registry) {
	if (registry->plugins == NULL)
		return;

	for (uint64_t i = 0; i < registry->count; i++) {
		struct plugin_handle *p = &registry->plugins[i];
		if (p->dl_handle != NULL) {
			dlclose(p->dl_handle);
			p->dl_handle = NULL;
		}
	}

	free(registry->plugins);
	registry->plugins = NULL;
	registry->count = 0;
}
