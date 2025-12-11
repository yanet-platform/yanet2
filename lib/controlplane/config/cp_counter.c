#include "cp_counter.h"

#include "common/container_of.h"
#include "common/memory.h"

#include "controlplane/config/defines.h"
#include "lib/controlplane/diag/diag.h"

#define COUNTER_REGISTRY_PREALLOC 8

int
cp_config_counter_storage_registry_init(
	struct memory_context *memory_context,
	struct cp_config_counter_storage_registry *registry
) {
	if (registry_init(
		    memory_context,
		    &registry->device_registry,
		    COUNTER_REGISTRY_PREALLOC
	    )) {
		NEW_ERROR("failed to initialize device registry for counter "
			  "storage");
		return -1;
	}

	SET_OFFSET_OF(&registry->memory_context, memory_context);
	return 0;
}

struct cp_config_counter_storage_device {
	struct registry_item item;
	char device_name[CP_DEVICE_NAME_LEN];
	struct counter_storage *counter_storage;
	struct registry pipeline_registry;
};

struct cp_config_counter_storage_pipeline {
	struct registry_item item;
	char pipeline_name[CP_PIPELINE_NAME_LEN];
	struct counter_storage *counter_storage;
	struct registry function_registry;
};

struct cp_config_counter_storage_function {
	struct registry_item item;
	char function_name[CP_FUNCTION_NAME_LEN];
	struct counter_storage *counter_storage;
	struct registry chain_registry;
};

struct cp_config_counter_storage_chain {
	struct registry_item item;
	char chain_name[CP_CHAIN_NAME_LEN];
	struct counter_storage *counter_storage;
	struct registry module_registry;
};

struct cp_config_counter_storage_module {
	struct registry_item item;
	char module_type[80];
	char module_name[CP_MODULE_NAME_LEN];
	struct counter_storage *counter_storage;
};

static void
cb_cp_config_counter_storage_module_free(
	struct registry_item *item, void *cb_data
) {
	struct memory_context *memory_context =
		(struct memory_context *)cb_data;

	struct cp_config_counter_storage_module *module = container_of(
		item, struct cp_config_counter_storage_module, item
	);

	memory_bfree(memory_context, module, sizeof(*module));
}

static void
cb_cp_config_counter_storage_chain_free(
	struct registry_item *item, void *cb_data
) {
	struct memory_context *memory_context =
		(struct memory_context *)cb_data;

	struct cp_config_counter_storage_chain *chain = container_of(
		item, struct cp_config_counter_storage_chain, item
	);

	registry_destroy(
		&chain->module_registry,
		cb_cp_config_counter_storage_module_free,
		memory_context
	);

	memory_bfree(memory_context, chain, sizeof(*chain));
}

static void
cb_cp_config_counter_storage_function_free(
	struct registry_item *item, void *cb_data
) {
	struct memory_context *memory_context =
		(struct memory_context *)cb_data;

	struct cp_config_counter_storage_function *function = container_of(
		item, struct cp_config_counter_storage_function, item
	);

	registry_destroy(
		&function->chain_registry,
		cb_cp_config_counter_storage_chain_free,
		memory_context
	);

	memory_bfree(memory_context, function, sizeof(*function));
}

static void
cb_cp_config_counter_storage_pipeline_free(
	struct registry_item *item, void *cb_data
) {
	struct memory_context *memory_context =
		(struct memory_context *)cb_data;

	struct cp_config_counter_storage_pipeline *pipeline = container_of(
		item, struct cp_config_counter_storage_pipeline, item
	);

	registry_destroy(
		&pipeline->function_registry,
		cb_cp_config_counter_storage_function_free,
		memory_context
	);

	memory_bfree(memory_context, pipeline, sizeof(*pipeline));
}

static void
cb_cp_config_counter_storage_device_free(
	struct registry_item *item, void *cb_data
) {
	struct memory_context *memory_context =
		(struct memory_context *)cb_data;

	struct cp_config_counter_storage_device *device = container_of(
		item, struct cp_config_counter_storage_device, item
	);

	registry_destroy(
		&device->pipeline_registry,
		cb_cp_config_counter_storage_pipeline_free,
		memory_context
	);

	memory_bfree(memory_context, device, sizeof(*device));
}

void
cp_config_counter_storage_registry_destroy(
	struct cp_config_counter_storage_registry *registry
) {
	registry_destroy(
		&registry->device_registry,
		cb_cp_config_counter_storage_device_free,
		ADDR_OF(&registry->memory_context)
	);
}

static int
compare_device_name(const struct registry_item *item, const void *data) {
	struct cp_config_counter_storage_device *device = container_of(
		item, struct cp_config_counter_storage_device, item
	);

	const char *device_name = (const char *)data;

	return strncmp(
		device->device_name, device_name, sizeof(device->device_name)
	);
}

static struct cp_config_counter_storage_device *
cp_config_counter_storage_registry_lookup_device_item(
	struct cp_config_counter_storage_registry *registry,
	const char *device_name
) {
	uint64_t index;
	if (registry_lookup(
		    &registry->device_registry,
		    compare_device_name,
		    device_name,
		    &index
	    )) {
		return NULL;
	}

	return container_of(
		registry_get(&registry->device_registry, index),
		struct cp_config_counter_storage_device,
		item
	);
}

struct counter_storage *
cp_config_counter_storage_registry_lookup_device(
	struct cp_config_counter_storage_registry *registry,
	const char *device_name
) {
	struct cp_config_counter_storage_device *device =
		cp_config_counter_storage_registry_lookup_device_item(
			registry, device_name
		);
	if (device == NULL)
		return NULL;

	return ADDR_OF(&device->counter_storage);
}

int
cp_config_counter_storage_registry_insert_device(
	struct cp_config_counter_storage_registry *registry,
	const char *device_name,
	struct counter_storage *counter_storage
) {
	struct cp_config_counter_storage_device *device =
		cp_config_counter_storage_registry_lookup_device_item(
			registry, device_name
		);
	if (device != NULL) {
		NEW_ERROR(
			"device '%s' already exists in counter storage "
			"registry",
			device_name
		);
		return -1;
	}

	struct memory_context *memory_context =
		ADDR_OF(&registry->memory_context);

	device = (struct cp_config_counter_storage_device *)memory_balloc(
		memory_context, sizeof(struct cp_config_counter_storage_device)
	);
	if (device == NULL) {
		NEW_ERROR(
			"failed to allocate memory for device '%s' in counter "
			"storage",
			device_name
		);
		return -1;
	}

	registry_item_init(&device->item);
	strtcpy(device->device_name, device_name, sizeof(device->device_name));
	if (registry_init(
		    memory_context,
		    &device->pipeline_registry,
		    COUNTER_REGISTRY_PREALLOC
	    )) {
		NEW_ERROR(
			"failed to initialize pipeline registry for device "
			"'%s'",
			device_name
		);
		goto error_init;
	}
	SET_OFFSET_OF(&device->counter_storage, counter_storage);

	if (registry_insert(&registry->device_registry, &device->item)) {
		NEW_ERROR(
			"failed to insert device '%s' into counter storage "
			"registry",
			device_name
		);
		goto error_insert;
	}

	return 0;

error_insert:
	registry_destroy(
		&device->pipeline_registry,
		cb_cp_config_counter_storage_pipeline_free,
		memory_context
	);

error_init:
	memory_bfree(
		memory_context,
		device,
		sizeof(struct cp_config_counter_storage_device)
	);

	return -1;
}

static int
compare_pipeline_name(const struct registry_item *item, const void *data) {
	struct cp_config_counter_storage_pipeline *pipeline = container_of(
		item, struct cp_config_counter_storage_pipeline, item
	);

	const char *pipeline_name = (const char *)data;

	return strncmp(
		pipeline->pipeline_name,
		pipeline_name,
		sizeof(pipeline->pipeline_name)
	);
}

static struct cp_config_counter_storage_pipeline *
cp_config_counter_storage_registry_lookup_pipeline_item(
	struct cp_config_counter_storage_device *device,
	const char *function_name
) {
	uint64_t index;
	if (registry_lookup(
		    &device->pipeline_registry,
		    compare_pipeline_name,
		    function_name,
		    &index
	    )) {
		return NULL;
	}

	return container_of(
		registry_get(&device->pipeline_registry, index),
		struct cp_config_counter_storage_pipeline,
		item
	);
}

struct counter_storage *
cp_config_counter_storage_registry_lookup_pipeline(
	struct cp_config_counter_storage_registry *registry,
	const char *device_name,
	const char *pipeline_name
) {
	struct cp_config_counter_storage_device *device =
		cp_config_counter_storage_registry_lookup_device_item(
			registry, device_name
		);
	if (device == NULL)
		return NULL;

	struct cp_config_counter_storage_pipeline *pipeline =
		cp_config_counter_storage_registry_lookup_pipeline_item(
			device, pipeline_name
		);
	if (pipeline == NULL)
		return NULL;

	return ADDR_OF(&pipeline->counter_storage);
}

int
cp_config_counter_storage_registry_insert_pipeline(
	struct cp_config_counter_storage_registry *registry,
	const char *device_name,
	const char *pipeline_name,
	struct counter_storage *counter_storage
) {
	struct cp_config_counter_storage_device *device =
		cp_config_counter_storage_registry_lookup_device_item(
			registry, device_name
		);

	if (device == NULL) {
		NEW_ERROR(
			"device '%s' not found in counter storage registry",
			device_name
		);
		return -1;
	}

	struct cp_config_counter_storage_pipeline *pipeline =
		cp_config_counter_storage_registry_lookup_pipeline_item(
			device, pipeline_name
		);

	if (pipeline != NULL) {
		NEW_ERROR(
			"pipeline '%s' already exists for device '%s' in "
			"counter storage",
			pipeline_name,
			device_name
		);
		return -1;
	}

	struct memory_context *memory_context =
		ADDR_OF(&registry->memory_context);

	pipeline = (struct cp_config_counter_storage_pipeline *)memory_balloc(
		memory_context,
		sizeof(struct cp_config_counter_storage_pipeline)
	);
	if (pipeline == NULL) {
		NEW_ERROR(
			"failed to allocate memory for pipeline '%s' on device "
			"'%s'",
			pipeline_name,
			device_name
		);
		return -1;
	}

	registry_item_init(&pipeline->item);
	strtcpy(pipeline->pipeline_name,
		pipeline_name,
		sizeof(pipeline->pipeline_name));
	if (registry_init(
		    memory_context,
		    &pipeline->function_registry,
		    COUNTER_REGISTRY_PREALLOC
	    )) {
		NEW_ERROR(
			"failed to initialize function registry for pipeline "
			"'%s'",
			pipeline_name
		);
		goto error_init;
	}
	SET_OFFSET_OF(&pipeline->counter_storage, counter_storage);

	if (registry_insert(&device->pipeline_registry, &pipeline->item)) {
		NEW_ERROR(
			"failed to insert pipeline '%s' into device '%s' "
			"registry",
			pipeline_name,
			device_name
		);
		goto error_insert;
	}

	return 0;

error_insert:
	registry_destroy(
		&pipeline->function_registry,
		cb_cp_config_counter_storage_function_free,
		memory_context
	);

error_init:
	memory_bfree(memory_context, pipeline, sizeof(*pipeline));

	return -1;
}

static int
compare_function_name(const struct registry_item *item, const void *data) {
	struct cp_config_counter_storage_function *function = container_of(
		item, struct cp_config_counter_storage_function, item
	);

	const char *function_name = (const char *)data;

	return strncmp(
		function->function_name,
		function_name,
		sizeof(function->function_name)
	);
}

static struct cp_config_counter_storage_function *
cp_config_counter_storage_registry_lookup_function_item(
	struct cp_config_counter_storage_pipeline *pipeline,
	const char *function_name
) {
	uint64_t index;
	if (registry_lookup(
		    &pipeline->function_registry,
		    compare_function_name,
		    function_name,
		    &index
	    )) {
		return NULL;
	}

	return container_of(
		registry_get(&pipeline->function_registry, index),
		struct cp_config_counter_storage_function,
		item
	);
}

struct counter_storage *
cp_config_counter_storage_registry_lookup_function(
	struct cp_config_counter_storage_registry *registry,
	const char *device_name,
	const char *pipeline_name,
	const char *function_name
) {
	struct cp_config_counter_storage_device *device =
		cp_config_counter_storage_registry_lookup_device_item(
			registry, device_name
		);
	if (device == NULL)
		return NULL;

	struct cp_config_counter_storage_pipeline *pipeline =
		cp_config_counter_storage_registry_lookup_pipeline_item(
			device, pipeline_name
		);
	if (pipeline == NULL)
		return NULL;

	struct cp_config_counter_storage_function *function =
		cp_config_counter_storage_registry_lookup_function_item(
			pipeline, function_name
		);
	if (function == NULL)
		return NULL;

	return ADDR_OF(&function->counter_storage);
}

int
cp_config_counter_storage_registry_insert_function(
	struct cp_config_counter_storage_registry *registry,
	const char *device_name,
	const char *pipeline_name,
	const char *function_name,
	struct counter_storage *counter_storage
) {
	struct cp_config_counter_storage_device *device =
		cp_config_counter_storage_registry_lookup_device_item(
			registry, device_name
		);
	if (device == NULL) {
		NEW_ERROR(
			"device '%s' not found in counter storage registry",
			device_name
		);
		return -1;
	}

	struct cp_config_counter_storage_pipeline *pipeline =
		cp_config_counter_storage_registry_lookup_pipeline_item(
			device, pipeline_name
		);

	if (pipeline == NULL) {
		NEW_ERROR(
			"pipeline '%s' not found on device '%s'",
			pipeline_name,
			device_name
		);
		return -1;
	}

	struct cp_config_counter_storage_function *function =
		cp_config_counter_storage_registry_lookup_function_item(
			pipeline, function_name
		);

	if (function != NULL) {
		NEW_ERROR(
			"function '%s' already exists for pipeline '%s' in "
			"counter storage",
			function_name,
			pipeline_name
		);
		return -1;
	}

	struct memory_context *memory_context =
		ADDR_OF(&registry->memory_context);

	function = (struct cp_config_counter_storage_function *)memory_balloc(
		memory_context,
		sizeof(struct cp_config_counter_storage_function)
	);
	if (function == NULL) {
		NEW_ERROR(
			"failed to allocate memory for function '%s' on "
			"pipeline '%s'",
			function_name,
			pipeline_name
		);
		return -1;
	}

	registry_item_init(&function->item);
	strtcpy(function->function_name,
		function_name,
		sizeof(function->function_name));
	if (registry_init(
		    memory_context,
		    &function->chain_registry,
		    COUNTER_REGISTRY_PREALLOC
	    )) {
		NEW_ERROR(
			"failed to initialize chain registry for function '%s'",
			function_name
		);
		goto error_init;
	}
	SET_OFFSET_OF(&function->counter_storage, counter_storage);

	if (registry_insert(&pipeline->function_registry, &function->item)) {
		NEW_ERROR(
			"failed to insert function '%s' into pipeline '%s' "
			"registry",
			function_name,
			pipeline_name
		);
		goto error_insert;
	}

	return 0;

error_insert:
	registry_destroy(
		&function->chain_registry,
		cb_cp_config_counter_storage_chain_free,
		memory_context
	);

error_init:
	memory_bfree(memory_context, function, sizeof(*function));

	return -1;
}

static int
compare_chain_name(const struct registry_item *item, const void *data) {
	struct cp_config_counter_storage_chain *chain = container_of(
		item, struct cp_config_counter_storage_chain, item
	);

	const char *chain_name = (const char *)data;

	return strncmp(
		chain->chain_name, chain_name, sizeof(chain->chain_name)
	);
}

static struct cp_config_counter_storage_chain *
cp_config_counter_storage_registry_lookup_chain_item(
	struct cp_config_counter_storage_function *function,
	const char *chain_name
) {
	uint64_t index;
	if (registry_lookup(
		    &function->chain_registry,
		    compare_chain_name,
		    chain_name,
		    &index
	    )) {
		return NULL;
	}

	return container_of(
		registry_get(&function->chain_registry, index),
		struct cp_config_counter_storage_chain,
		item
	);
}

struct counter_storage *
cp_config_counter_storage_registry_lookup_chain(
	struct cp_config_counter_storage_registry *registry,
	const char *device_name,
	const char *pipeline_name,
	const char *function_name,
	const char *chain_name
) {
	struct cp_config_counter_storage_device *device =
		cp_config_counter_storage_registry_lookup_device_item(
			registry, device_name
		);
	if (device == NULL)
		return NULL;

	struct cp_config_counter_storage_pipeline *pipeline =
		cp_config_counter_storage_registry_lookup_pipeline_item(
			device, pipeline_name
		);
	if (pipeline == NULL)
		return NULL;

	struct cp_config_counter_storage_function *function =
		cp_config_counter_storage_registry_lookup_function_item(
			pipeline, function_name
		);
	if (function == NULL)
		return NULL;

	struct cp_config_counter_storage_chain *chain =
		cp_config_counter_storage_registry_lookup_chain_item(
			function, chain_name
		);
	if (chain == NULL)
		return NULL;

	return ADDR_OF(&chain->counter_storage);
}

int
cp_config_counter_storage_registry_insert_chain(
	struct cp_config_counter_storage_registry *registry,
	const char *device_name,
	const char *pipeline_name,
	const char *function_name,
	const char *chain_name,
	struct counter_storage *counter_storage
) {
	struct cp_config_counter_storage_device *device =
		cp_config_counter_storage_registry_lookup_device_item(
			registry, device_name
		);
	if (device == NULL) {
		NEW_ERROR(
			"device '%s' not found in counter storage registry",
			device_name
		);
		return -1;
	}

	struct cp_config_counter_storage_pipeline *pipeline =
		cp_config_counter_storage_registry_lookup_pipeline_item(
			device, pipeline_name
		);
	if (pipeline == NULL) {
		NEW_ERROR(
			"pipeline '%s' not found on device '%s'",
			pipeline_name,
			device_name
		);
		return -1;
	}

	struct cp_config_counter_storage_function *function =
		cp_config_counter_storage_registry_lookup_function_item(
			pipeline, function_name
		);
	if (function == NULL) {
		NEW_ERROR(
			"function '%s' not found on pipeline '%s'",
			function_name,
			pipeline_name
		);
		return -1;
	}

	struct cp_config_counter_storage_chain *chain =
		cp_config_counter_storage_registry_lookup_chain_item(
			function, chain_name
		);
	if (chain != NULL) {
		return -1;
	}

	struct memory_context *memory_context =
		ADDR_OF(&registry->memory_context);

	chain = (struct cp_config_counter_storage_chain *)memory_balloc(
		memory_context, sizeof(struct cp_config_counter_storage_chain)
	);
	if (chain == NULL) {
		NEW_ERROR(
			"failed to allocate memory for chain '%s' on function "
			"'%s'",
			chain_name,
			function_name
		);
		return -1;
	}

	registry_item_init(&chain->item);
	strtcpy(chain->chain_name, chain_name, sizeof(chain->chain_name));
	if (registry_init(
		    memory_context,
		    &chain->module_registry,
		    COUNTER_REGISTRY_PREALLOC
	    )) {
		NEW_ERROR(
			"failed to initialize module registry for chain '%s'",
			chain_name
		);
		goto error_init;
	}
	SET_OFFSET_OF(&chain->counter_storage, counter_storage);

	if (registry_insert(&function->chain_registry, &chain->item)) {
		NEW_ERROR(
			"failed to insert chain '%s' into function '%s' "
			"registry",
			chain_name,
			function_name
		);
		goto error_insert;
	}

	return 0;

error_insert:
	registry_destroy(
		&chain->module_registry,
		cb_cp_config_counter_storage_module_free,
		memory_context
	);

error_init:
	memory_bfree(memory_context, chain, sizeof(*chain));

	return -1;
}

struct module_item_key {
	const char *module_type;
	const char *module_name;
};

static int
compare_module_type_name(const struct registry_item *item, const void *data) {
	struct cp_config_counter_storage_module *module = container_of(
		item, struct cp_config_counter_storage_module, item
	);

	const struct module_item_key *key =
		(const struct module_item_key *)data;

	int rc =
		strncmp(module->module_type,
			key->module_type,
			sizeof(module->module_type));
	if (rc)
		return rc;

	return strncmp(
		module->module_name,
		key->module_name,
		sizeof(module->module_name)
	);
}

static struct cp_config_counter_storage_module *
cp_config_counter_storage_registry_lookup_module_item(
	struct cp_config_counter_storage_chain *chain,
	const char *module_type,
	const char *module_name
) {
	struct module_item_key cmp_key = {
		.module_type = module_type,
		.module_name = module_name,
	};

	uint64_t index;
	if (registry_lookup(
		    &chain->module_registry,
		    compare_module_type_name,
		    &cmp_key,
		    &index
	    )) {
		return NULL;
	}

	return container_of(
		registry_get(&chain->module_registry, index),
		struct cp_config_counter_storage_module,
		item
	);
}

struct counter_storage *
cp_config_counter_storage_registry_lookup_module(
	struct cp_config_counter_storage_registry *registry,
	const char *device_name,
	const char *pipeline_name,
	const char *function_name,
	const char *chain_name,
	const char *module_type,
	const char *module_name
) {
	struct cp_config_counter_storage_device *device =
		cp_config_counter_storage_registry_lookup_device_item(
			registry, device_name
		);
	if (device == NULL)
		return NULL;

	struct cp_config_counter_storage_pipeline *pipeline =
		cp_config_counter_storage_registry_lookup_pipeline_item(
			device, pipeline_name
		);
	if (pipeline == NULL)
		return NULL;

	struct cp_config_counter_storage_function *function =
		cp_config_counter_storage_registry_lookup_function_item(
			pipeline, function_name
		);
	if (function == NULL)
		return NULL;

	struct cp_config_counter_storage_chain *chain =
		cp_config_counter_storage_registry_lookup_chain_item(
			function, chain_name
		);
	if (chain == NULL)
		return NULL;

	struct cp_config_counter_storage_module *module =
		cp_config_counter_storage_registry_lookup_module_item(
			chain, module_type, module_name
		);
	if (module == NULL)
		return NULL;

	return ADDR_OF(&module->counter_storage);
}

int
cp_config_counter_storage_registry_insert_module(
	struct cp_config_counter_storage_registry *registry,
	const char *device_name,
	const char *pipeline_name,
	const char *function_name,
	const char *chain_name,
	const char *module_type,
	const char *module_name,
	struct counter_storage *counter_storage
) {
	struct cp_config_counter_storage_device *device =
		cp_config_counter_storage_registry_lookup_device_item(
			registry, device_name
		);
	if (device == NULL) {
		NEW_ERROR(
			"device '%s' not found in counter storage registry",
			device_name
		);
		return -1;
	}

	struct cp_config_counter_storage_pipeline *pipeline =
		cp_config_counter_storage_registry_lookup_pipeline_item(
			device, pipeline_name
		);
	if (pipeline == NULL) {
		NEW_ERROR(
			"pipeline '%s' not found on device '%s'",
			pipeline_name,
			device_name
		);
		return -1;
	}

	struct cp_config_counter_storage_function *function =
		cp_config_counter_storage_registry_lookup_function_item(
			pipeline, function_name
		);
	if (function == NULL) {
		NEW_ERROR(
			"function '%s' not found on pipeline '%s'",
			function_name,
			pipeline_name
		);
		return -1;
	}

	struct cp_config_counter_storage_chain *chain =
		cp_config_counter_storage_registry_lookup_chain_item(
			function, chain_name
		);
	if (chain == NULL) {
		NEW_ERROR(
			"chain '%s' not found on function '%s'",
			chain_name,
			function_name
		);
		return -1;
	}

	struct cp_config_counter_storage_module *module =
		cp_config_counter_storage_registry_lookup_module_item(
			chain, module_type, module_name
		);
	if (module != NULL) {
		NEW_ERROR(
			"module '%s:%s' already exists for chain '%s' in "
			"counter storage",
			module_type,
			module_name,
			chain_name
		);
		return -1;
	}

	struct memory_context *memory_context =
		ADDR_OF(&registry->memory_context);

	module = (struct cp_config_counter_storage_module *)memory_balloc(
		memory_context, sizeof(struct cp_config_counter_storage_module)
	);
	if (module == NULL) {
		NEW_ERROR(
			"failed to allocate memory for module '%s:%s' on chain "
			"'%s'",
			module_type,
			module_name,
			chain_name
		);
		return -1;
	}

	registry_item_init(&module->item);
	strtcpy(module->module_type, module_type, sizeof(module->module_type));
	strtcpy(module->module_name, module_name, sizeof(module->module_name));
	SET_OFFSET_OF(&module->counter_storage, counter_storage);

	if (registry_insert(&chain->module_registry, &module->item)) {
		NEW_ERROR(
			"failed to insert module '%s:%s' into chain '%s' "
			"registry",
			module_type,
			module_name,
			chain_name
		);
		goto error;
	}

	return 0;

error:
	memory_bfree(memory_context, module, sizeof(*module));

	return -1;
}
