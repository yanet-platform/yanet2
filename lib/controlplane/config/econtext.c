#include "econtext.h"

#include <string.h>

// cp_config and cp_config_gen
#include "lib/controlplane/config/zone.h"

static void
module_ectx_free(
	struct cp_config_gen *cp_config_gen, struct module_ectx *module_ectx
) {
	struct cp_config *cp_config = ADDR_OF(&cp_config_gen->cp_config);
	struct memory_context *memory_context = &cp_config->memory_context;

	struct counter_storage *counter_storage =
		ADDR_OF(&module_ectx->counter_storage);
	if (counter_storage != NULL)
		counter_storage_free(counter_storage);

	memory_bfree(memory_context, module_ectx, sizeof(struct module_ectx));
}

static struct module_ectx *
module_ectx_create(
	struct cp_config_gen *cp_config_gen,
	struct cp_module *cp_module,
	struct cp_config_gen *old_config_gen,
	struct device_ectx *device_ectx,
	struct pipeline_ectx *pipeline_ectx,
	struct function_ectx *function_ectx,
	struct chain_ectx *chain_ectx
) {
	struct cp_config *cp_config = ADDR_OF(&cp_config_gen->cp_config);
	struct memory_context *memory_context = &cp_config->memory_context;

	size_t ectx_size = sizeof(struct module_ectx);
	struct module_ectx *module_ectx =
		(struct module_ectx *)memory_balloc(memory_context, ectx_size);
	if (module_ectx == NULL)
		return NULL;

	memset(module_ectx, 0, ectx_size);
	SET_OFFSET_OF(&module_ectx->module, cp_module);

	struct cp_device *cp_device = ADDR_OF(&device_ectx->device);
	struct cp_pipeline *cp_pipeline = ADDR_OF(&pipeline_ectx->pipeline);
	struct cp_function *cp_function = ADDR_OF(&function_ectx->function);
	struct cp_chain *cp_chain = ADDR_OF(&chain_ectx->chain);

	struct counter_storage *old_counter_storage =
		cp_config_counter_storage_registry_lookup_module(
			&old_config_gen->counter_storage_registry,
			cp_device->name,
			cp_pipeline->name,
			cp_function->name,
			cp_chain->name,
			cp_module->type,
			cp_module->name
		);

	struct counter_storage *counter_storage = counter_storage_spawn(
		memory_context,
		&cp_config->counter_storage_allocator,
		old_counter_storage,
		&cp_module->counter_registry
	);
	if (counter_storage == NULL)
		goto error;

	if (cp_config_counter_storage_registry_insert_module(
		    &cp_config_gen->counter_storage_registry,
		    cp_device->name,
		    cp_pipeline->name,
		    cp_function->name,
		    cp_chain->name,
		    cp_module->type,
		    cp_module->name,
		    counter_storage
	    )) {
		goto error;
	}

	SET_OFFSET_OF(&module_ectx->counter_storage, counter_storage);

	return module_ectx;

error:

	memory_bfree(memory_context, module_ectx, ectx_size);

	return NULL;
}

static void
chain_ectx_free(
	struct cp_config_gen *cp_config_gen, struct chain_ectx *chain_ectx
) {
	struct cp_config *cp_config = ADDR_OF(&cp_config_gen->cp_config);
	struct memory_context *memory_context = &cp_config->memory_context;

	for (uint64_t idx = 0; idx < chain_ectx->length; ++idx) {
		struct module_ectx *module_ectx =
			ADDR_OF(chain_ectx->modules + idx);
		if (module_ectx == NULL)
			continue;

		module_ectx_free(cp_config_gen, module_ectx);
	}

	struct counter_storage *counter_storage =
		ADDR_OF(&chain_ectx->counter_storage);
	if (counter_storage != NULL)
		counter_storage_free(counter_storage);

	memory_bfree(
		memory_context,
		chain_ectx,
		sizeof(struct chain_ectx) +
			sizeof(struct module_ectx *) * chain_ectx->length
	);
}

static struct chain_ectx *
chain_ectx_create(
	struct cp_config_gen *cp_config_gen,
	struct cp_chain *cp_chain,
	struct cp_config_gen *old_config_gen,
	struct device_ectx *device_ectx,
	struct pipeline_ectx *pipeline_ectx,
	struct function_ectx *function_ectx
) {
	struct cp_config *cp_config = ADDR_OF(&cp_config_gen->cp_config);
	struct memory_context *memory_context = &cp_config->memory_context;

	uint64_t ectx_size = sizeof(struct chain_ectx) +
			     sizeof(struct module_ectx *) * cp_chain->length;
	struct chain_ectx *chain_ectx =
		(struct chain_ectx *)memory_balloc(memory_context, ectx_size);
	if (chain_ectx == NULL)
		return NULL;

	memset(chain_ectx, 0, ectx_size);
	SET_OFFSET_OF(&chain_ectx->chain, cp_chain);
	chain_ectx->length = cp_chain->length;

	struct cp_device *cp_device = ADDR_OF(&device_ectx->device);
	struct cp_pipeline *cp_pipeline = ADDR_OF(&pipeline_ectx->pipeline);
	struct cp_function *cp_function = ADDR_OF(&function_ectx->function);

	struct counter_storage *old_counter_storage =
		cp_config_counter_storage_registry_lookup_chain(
			&old_config_gen->counter_storage_registry,
			cp_device->name,
			cp_pipeline->name,
			cp_function->name,
			cp_chain->name
		);

	struct counter_storage *counter_storage = counter_storage_spawn(
		memory_context,
		&cp_config->counter_storage_allocator,
		old_counter_storage,
		&cp_chain->counter_registry
	);
	if (counter_storage == NULL)
		goto error;

	if (cp_config_counter_storage_registry_insert_chain(
		    &cp_config_gen->counter_storage_registry,
		    cp_device->name,
		    cp_pipeline->name,
		    cp_function->name,
		    cp_chain->name,
		    counter_storage
	    )) {
		goto error;
	}

	SET_OFFSET_OF(&chain_ectx->counter_storage, counter_storage);

	for (uint64_t idx = 0; idx < cp_chain->length; ++idx) {

		struct cp_module *cp_module = cp_config_gen_lookup_module(
			cp_config_gen,
			cp_chain->modules[idx].type,
			cp_chain->modules[idx].name
		);

		if (cp_module == NULL)
			goto error;

		struct module_ectx *module_ectx = module_ectx_create(
			cp_config_gen,
			cp_module,
			old_config_gen,
			device_ectx,
			pipeline_ectx,
			function_ectx,
			chain_ectx
		);
		if (module_ectx == NULL)
			goto error;

		SET_OFFSET_OF(chain_ectx->modules + idx, module_ectx);
	}

	return chain_ectx;

error:

	chain_ectx_free(cp_config_gen, chain_ectx);
	return NULL;
}

static void
function_ectx_free(
	struct cp_config_gen *cp_config_gen, struct function_ectx *function_ectx
) {
	struct cp_config *cp_config = ADDR_OF(&cp_config_gen->cp_config);
	struct memory_context *memory_context = &cp_config->memory_context;

	struct chain_ectx **chains = ADDR_OF(&function_ectx->chains);
	if (chains != NULL) {
		for (uint64_t idx = 0; idx < function_ectx->chain_count;
		     ++idx) {
			struct chain_ectx *chain_ectx = ADDR_OF(chains + idx);
			if (chain_ectx == NULL)
				continue;

			chain_ectx_free(cp_config_gen, chain_ectx);
		}
		memory_bfree(
			memory_context,
			chains,
			sizeof(struct chain_ectx *) * function_ectx->chain_count
		);
	}

	struct counter_storage *counter_storage =
		ADDR_OF(&function_ectx->counter_storage);
	if (counter_storage != NULL)
		counter_storage_free(counter_storage);

	size_t ectx_size =
		sizeof(struct function_ectx) +
		sizeof(struct chain_ectx *) * function_ectx->chain_map_size;
	memory_bfree(memory_context, function_ectx, ectx_size);
}

static struct function_ectx *
function_ectx_create(
	struct cp_config_gen *cp_config_gen,
	struct cp_function *cp_function,
	struct cp_config_gen *old_config_gen,
	struct device_ectx *device_ectx,
	struct pipeline_ectx *pipeline_ectx
) {
	struct cp_config *cp_config = ADDR_OF(&cp_config_gen->cp_config);
	struct memory_context *memory_context = &cp_config->memory_context;

	uint64_t weight_sum = 0;
	for (uint64_t idx = 0; idx < cp_function->chain_count; ++idx) {
		weight_sum += cp_function->chains[idx].weight;
	}

	size_t ectx_size = sizeof(struct function_ectx) +
			   sizeof(struct chain_ectx *) * weight_sum;

	struct function_ectx *function_ectx = (struct function_ectx *)
		memory_balloc(memory_context, ectx_size);
	if (function_ectx == NULL)
		return NULL;

	memset(function_ectx, 0, ectx_size);
	SET_OFFSET_OF(&function_ectx->function, cp_function);
	function_ectx->chain_count = cp_function->chain_count;

	struct chain_ectx **chains = (struct chain_ectx **)memory_balloc(
		memory_context,
		sizeof(struct chain_ectx **) * function_ectx->chain_count
	);
	if (chains == NULL)
		goto error;
	memset(chains,
	       0,
	       sizeof(struct chain_ectx **) * function_ectx->chain_count);
	SET_OFFSET_OF(&function_ectx->chains, chains);

	struct cp_device *cp_device = ADDR_OF(&device_ectx->device);
	struct cp_pipeline *cp_pipeline = ADDR_OF(&pipeline_ectx->pipeline);

	struct counter_storage *old_counter_storage =
		cp_config_counter_storage_registry_lookup_function(
			&old_config_gen->counter_storage_registry,
			cp_device->name,
			cp_pipeline->name,
			cp_function->name
		);

	struct counter_storage *counter_storage = counter_storage_spawn(
		memory_context,
		&cp_config->counter_storage_allocator,
		old_counter_storage,
		&cp_function->counter_registry
	);
	if (counter_storage == NULL)
		goto error;

	if (cp_config_counter_storage_registry_insert_function(
		    &cp_config_gen->counter_storage_registry,
		    cp_device->name,
		    cp_pipeline->name,
		    cp_function->name,
		    counter_storage
	    )) {
		goto error;
	}

	SET_OFFSET_OF(&function_ectx->counter_storage, counter_storage);

	uint64_t pos = 0;
	for (uint64_t idx = 0; idx < cp_function->chain_count; ++idx) {
		struct cp_chain *cp_chain =
			ADDR_OF(&cp_function->chains[idx].cp_chain);
		struct chain_ectx *chain_ectx = chain_ectx_create(
			cp_config_gen,
			cp_chain,
			old_config_gen,
			device_ectx,
			pipeline_ectx,
			function_ectx
		);
		if (chain_ectx == NULL)
			goto error;
		SET_OFFSET_OF(chains + idx, chain_ectx);

		for (uint64_t weight_idx = 0;
		     weight_idx < cp_function->chains[idx].weight;
		     ++weight_idx) {
			SET_OFFSET_OF(
				function_ectx->chain_map + pos, chain_ectx
			);
			++pos;
		}
	}

	return function_ectx;

error:
	function_ectx_free(cp_config_gen, function_ectx);

	return NULL;
}

static void
pipeline_ectx_free(
	struct cp_config_gen *cp_config_gen, struct pipeline_ectx *pipeline_ectx
) {
	struct cp_config *cp_config = ADDR_OF(&cp_config_gen->cp_config);
	struct memory_context *memory_context = &cp_config->memory_context;

	for (uint64_t idx = 0; idx < pipeline_ectx->length; ++idx) {
		struct function_ectx *function_ectx =
			ADDR_OF(pipeline_ectx->functions + idx);
		if (function_ectx == NULL)
			continue;

		function_ectx_free(cp_config_gen, function_ectx);
	}

	struct counter_storage *counter_storage =
		ADDR_OF(&pipeline_ectx->counter_storage);
	if (counter_storage != NULL)
		counter_storage_free(counter_storage);

	size_t ectx_size =
		sizeof(struct pipeline_ectx) +
		sizeof(struct function_ectx *) * pipeline_ectx->length;
	memory_bfree(memory_context, pipeline_ectx, ectx_size);
}

static struct pipeline_ectx *
pipeline_ectx_create(
	struct cp_config_gen *cp_config_gen,
	struct cp_pipeline *cp_pipeline,
	struct cp_config_gen *old_config_gen,
	struct device_ectx *device_ectx
) {
	struct cp_config *cp_config = ADDR_OF(&cp_config_gen->cp_config);
	struct memory_context *memory_context = &cp_config->memory_context;

	size_t ectx_size = sizeof(struct pipeline_ectx) +
			   sizeof(struct function_ectx *) * cp_pipeline->length;

	struct pipeline_ectx *pipeline_ectx = (struct pipeline_ectx *)
		memory_balloc(memory_context, ectx_size);
	if (pipeline_ectx == NULL)
		return NULL;
	memset(pipeline_ectx, 0, ectx_size);
	SET_OFFSET_OF(&pipeline_ectx->pipeline, cp_pipeline);
	pipeline_ectx->length = cp_pipeline->length;

	struct cp_device *cp_device = ADDR_OF(&device_ectx->device);

	struct counter_storage *old_counter_storage =
		cp_config_counter_storage_registry_lookup_pipeline(
			&old_config_gen->counter_storage_registry,
			cp_device->name,
			cp_pipeline->name
		);

	struct counter_storage *counter_storage = counter_storage_spawn(
		memory_context,
		&cp_config->counter_storage_allocator,
		old_counter_storage,
		&cp_pipeline->counter_registry
	);
	if (counter_storage == NULL)
		goto error;

	if (cp_config_counter_storage_registry_insert_pipeline(
		    &cp_config_gen->counter_storage_registry,
		    cp_device->name,
		    cp_pipeline->name,
		    counter_storage
	    )) {
		goto error;
	}

	SET_OFFSET_OF(&pipeline_ectx->counter_storage, counter_storage);

	for (uint64_t idx = 0; idx < cp_pipeline->length; ++idx) {
		struct cp_function *cp_function = cp_config_gen_lookup_function(
			cp_config_gen, cp_pipeline->functions[idx].name
		);

		struct function_ectx *function_ectx = function_ectx_create(
			cp_config_gen,
			cp_function,
			old_config_gen,
			device_ectx,
			pipeline_ectx
		);
		if (function_ectx == NULL)
			goto error;

		SET_OFFSET_OF(pipeline_ectx->functions + idx, function_ectx);
	}

	return pipeline_ectx;

error:

	pipeline_ectx_free(cp_config_gen, pipeline_ectx);

	return NULL;
}

static void
device_ectx_free(
	struct cp_config_gen *cp_config_gen, struct device_ectx *device_ectx
) {
	struct cp_config *cp_config = ADDR_OF(&cp_config_gen->cp_config);
	struct memory_context *memory_context = &cp_config->memory_context;

	struct pipeline_ectx **pipelines = ADDR_OF(&device_ectx->pipelines);
	for (uint64_t idx = 0; idx < device_ectx->pipeline_count; ++idx) {
		struct pipeline_ectx *pipeline_ectx = ADDR_OF(pipelines + idx);
		if (pipeline_ectx == NULL)
			continue;
		pipeline_ectx_free(cp_config_gen, pipeline_ectx);
	}

	struct counter_storage *counter_storage =
		ADDR_OF(&device_ectx->counter_storage);
	if (counter_storage != NULL)
		counter_storage_free(counter_storage);

	memory_bfree(
		memory_context,
		pipelines,
		sizeof(struct pipeline_ectx *) * device_ectx->pipeline_count
	);

	size_t ectx_size =
		sizeof(struct device_ectx) +
		sizeof(struct pipeline_ectx *) * device_ectx->pipeline_map_size;

	memory_bfree(memory_context, device_ectx, ectx_size);
}

static struct device_ectx *
device_ectx_create(
	struct cp_config_gen *cp_config_gen,
	struct cp_device *cp_device,
	struct cp_config_gen *old_config_gen
) {
	struct cp_config *cp_config = ADDR_OF(&cp_config_gen->cp_config);
	struct memory_context *memory_context = &cp_config->memory_context;

	uint64_t weight_sum = 0;
	for (uint64_t idx = 0; idx < cp_device->pipeline_count; ++idx) {
		weight_sum += cp_device->pipeline_weights[idx].weight;
	}

	size_t ectx_size = sizeof(struct device_ectx) +
			   sizeof(struct pipeline_ectx *) * weight_sum;

	struct device_ectx *device_ectx =
		(struct device_ectx *)memory_balloc(memory_context, ectx_size);
	if (device_ectx == NULL)
		return NULL;

	memset(device_ectx, 0, ectx_size);
	SET_OFFSET_OF(&device_ectx->device, cp_device);
	device_ectx->pipeline_count = cp_device->pipeline_count;
	struct pipeline_ectx **pipelines =
		(struct pipeline_ectx **)memory_balloc(
			memory_context,
			sizeof(struct pipeline_ectx *) *
				device_ectx->pipeline_count
		);
	if (pipelines == NULL)
		goto error;
	memset(pipelines,
	       0,
	       sizeof(struct pipeline_ectx *) * device_ectx->pipeline_count);
	SET_OFFSET_OF(&device_ectx->pipelines, pipelines);

	struct counter_storage *old_counter_storage =
		cp_config_counter_storage_registry_lookup_device(
			&old_config_gen->counter_storage_registry,
			cp_device->name
		);

	struct counter_storage *counter_storage = counter_storage_spawn(
		memory_context,
		&cp_config->counter_storage_allocator,
		old_counter_storage,
		&cp_device->counter_registry
	);
	if (counter_storage == NULL)
		goto error;

	if (cp_config_counter_storage_registry_insert_device(
		    &cp_config_gen->counter_storage_registry,
		    cp_device->name,
		    counter_storage
	    )) {
		goto error;
	}

	SET_OFFSET_OF(&device_ectx->counter_storage, counter_storage);

	device_ectx->pipeline_map_size = weight_sum;
	uint64_t pos = 0;
	for (uint64_t idx = 0; idx < cp_device->pipeline_count; ++idx) {
		struct cp_pipeline *cp_pipeline = cp_config_gen_lookup_pipeline(
			cp_config_gen, cp_device->pipeline_weights[idx].name
		);
		if (cp_pipeline == NULL) {
			goto error;
		}
		struct pipeline_ectx *pipeline_ectx = pipeline_ectx_create(
			cp_config_gen, cp_pipeline, old_config_gen, device_ectx
		);
		if (pipeline_ectx == NULL)
			goto error;

		SET_OFFSET_OF(pipelines + idx, pipeline_ectx);

		for (uint64_t weight_idx = 0;
		     weight_idx < cp_device->pipeline_weights[idx].weight;
		     ++weight_idx) {
			SET_OFFSET_OF(
				device_ectx->pipeline_map + pos, pipeline_ectx
			);
			++pos;
		}
	}

	return device_ectx;

error:
	device_ectx_free(cp_config_gen, device_ectx);

	return NULL;
}

void
config_ectx_free(
	struct cp_config_gen *cp_config_gen, struct config_ectx *config_ectx
) {
	struct cp_config *cp_config = ADDR_OF(&cp_config_gen->cp_config);
	struct memory_context *memory_context = &cp_config->memory_context;

	for (uint64_t device_idx = 0; device_idx < config_ectx->device_count;
	     ++device_idx) {

		struct device_ectx *device_ectx =
			ADDR_OF(config_ectx->devices + device_idx);

		if (device_ectx != NULL) {
			device_ectx_free(cp_config_gen, device_ectx);
		}
	}

	size_t ectx_size =
		sizeof(struct config_ectx) +
		sizeof(struct device_ectx *) * config_ectx->device_count;

	memory_bfree(memory_context, config_ectx, ectx_size);
}

struct config_ectx *
config_ectx_create(
	struct cp_config_gen *cp_config_gen,
	struct cp_config_gen *old_config_gen
) {
	struct cp_config *cp_config = ADDR_OF(&cp_config_gen->cp_config);
	struct memory_context *memory_context = &cp_config->memory_context;

	size_t ectx_size = sizeof(struct config_ectx) +
			   sizeof(struct device_ectx *) *
				   cp_device_registry_capacity(
					   &cp_config_gen->device_registry
				   );

	struct config_ectx *config_ectx =
		(struct config_ectx *)memory_balloc(memory_context, ectx_size);
	if (config_ectx == NULL)
		return config_ectx;
	memset(config_ectx, 0, ectx_size);
	config_ectx->device_count =
		cp_device_registry_capacity(&cp_config_gen->device_registry);

	for (uint64_t device_idx = 0;
	     // FIXME: cp_config_gen_device_count
	     device_idx <
	     cp_device_registry_capacity(&cp_config_gen->device_registry);
	     ++device_idx) {

		struct cp_device *cp_device =
			cp_config_gen_get_device(cp_config_gen, device_idx);
		if (cp_device == NULL) {
			config_ectx->devices[device_idx] = NULL;
			continue;
		}

		struct device_ectx *device_ectx = device_ectx_create(
			cp_config_gen, cp_device, old_config_gen
		);
		if (device_ectx == NULL)
			goto error;
		SET_OFFSET_OF(config_ectx->devices + device_idx, device_ectx);
	}

	return config_ectx;

error:
	config_ectx_free(cp_config_gen, config_ectx);

	return NULL;
}
