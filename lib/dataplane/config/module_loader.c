#include "module_loader.h"

#include <dlfcn.h>
#include <stdio.h>
#include <stdlib.h>

#include "common/exp_array.h"
#include "common/memory_address.h"
#include "common/strutils.h"
#include "lib/dataplane/config/zone.h"
#include "lib/dataplane/device/device.h"
#include "lib/dataplane/module/module.h"
#include "lib/logging/log.h"

int
dp_load_module(struct dp_config *dp_config, void *bin_hndl, const char *name) {
	LOG(INFO, "load module %s", name);
	char loader_name[64];
	snprintf(loader_name, sizeof(loader_name), "%s%s", "new_module_", name);
	module_load_handler loader =
		(module_load_handler)dlsym(bin_hndl, loader_name);
	if (loader == NULL) {
		LOG(ERROR, "failed to load dyn symbol %s", loader_name);
		return -1;
	}
	struct module *module = loader();

	struct dp_module *dp_modules = ADDR_OF(&dp_config->dp_modules);
	if (mem_array_expand_exp(
		    &dp_config->memory_context,
		    (void **)&dp_modules,
		    sizeof(*dp_modules),
		    &dp_config->module_count
	    )) {
		LOG(ERROR, "failed to allocate memory for module %s", name);
		// FIXME: free module
		return -1;
	}

	struct dp_module *dp_module = dp_modules + dp_config->module_count - 1;

	strtcpy(dp_module->name, module->name, sizeof(dp_module->name));
	dp_module->handler = module->handler;

	SET_OFFSET_OF(&dp_config->dp_modules, dp_modules);

	free(module);

	return 0;
}

int
dp_load_device(struct dp_config *dp_config, void *bin_hndl, const char *name) {
	LOG(INFO, "load device %s", name);
	char loader_name[64];
	snprintf(loader_name, sizeof(loader_name), "%s%s", "new_device_", name);
	device_load_handler loader =
		(device_load_handler)dlsym(bin_hndl, loader_name);
	if (loader == NULL) {
		LOG(ERROR, "failed to load dyn symbol %s", loader_name);
		return -1;
	}
	struct device *device = loader();

	struct dp_device *dp_devices = ADDR_OF(&dp_config->dp_devices);
	if (mem_array_expand_exp(
		    &dp_config->memory_context,
		    (void **)&dp_devices,
		    sizeof(*dp_devices),
		    &dp_config->device_count
	    )) {
		LOG(ERROR, "failed to allocate memory for device %s", name);
		// FIXME: free device
		return -1;
	}

	struct dp_device *dp_device = dp_devices + dp_config->device_count - 1;

	strtcpy(dp_device->name, device->name, sizeof(dp_device->name));
	dp_device->input_handler = device->input_handler;
	dp_device->output_handler = device->output_handler;

	SET_OFFSET_OF(&dp_config->dp_devices, dp_devices);

	free(device);

	return 0;
}
