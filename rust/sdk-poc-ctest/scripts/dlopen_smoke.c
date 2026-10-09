// Loads the Rust plugin through the dataplane's own plugin loader.
//
// The loader checks the exported ABI version and resolves the plugin's
// undefined symbols against this executable, which exports the dataplane's
// tunnel stripper like the real dataplane binary does.

#include <dlfcn.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "lib/dataplane/config/plugin_loader.h"
#include "lib/dataplane/module/module.h"
#include "lib/dataplane/packet/decap.h"

int
main(int argc, char **argv) {
	if (argc != 2) {
		fprintf(stderr, "usage: %s PLUGIN_DIR\n", argv[0]);
		return 2;
	}
	// Keeps the stripper in the executable for the plugin to bind to.
	volatile void *keep = (void *)&packet_decap;
	(void)keep;

	struct plugin_registry registry;
	if (dp_load_plugins(argv[1], &registry) || registry.count != 1) {
		fprintf(stderr, "plugin loading failed\n");
		return 1;
	}
	module_load_handler ctor = (module_load_handler
	)dlsym(registry.plugins[0].dl_handle, "new_module_decap");
	struct module *module = ctor != NULL ? ctor() : NULL;
	if (module == NULL || strcmp(module->name, "decap") != 0 ||
	    module->handler == NULL) {
		fprintf(stderr, "bad module descriptor\n");
		return 1;
	}
	printf("plugin %s: module %s, handler %s, commit %s, abi %u\n",
	       registry.plugins[0].name,
	       module->name,
	       module->handler ? "set" : "unset",
	       module->commit_handler ? "set" : "unset",
	       (unsigned)YANET_MODULE_ABI_VERSION);
	free(module);
	dp_unload_plugins(&registry);
	return 0;
}
