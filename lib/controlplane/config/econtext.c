#include "econtext.h"

#include <string.h>

// cp_config and cp_config_gen
#include "lib/controlplane/config/zone.h"
#include "lib/controlplane/diag/diag.h"

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

	uint64_t *cm_index = ADDR_OF(&module_ectx->cm_index);
	if (cm_index != NULL) {
		memory_bfree(
			memory_context,
			cm_index,
			sizeof(uint64_t) * module_ectx->cm_index_size
		);
	}

	uint64_t *mc_index = ADDR_OF(&module_ectx->mc_index);
	if (mc_index != NULL) {
		memory_bfree(
			memory_context,
			mc_index,
			sizeof(uint64_t) * module_ectx->mc_index_size
		);
	}

	memory_bfree(memory_context, module_ectx, sizeof(struct module_ectx));
}

static struct module_ectx *
module_ectx_create(
	struct cp_config_gen *cp_config_gen,
	struct cp_module *cp_module,
	struct cp_config_gen *old_config_gen,
	struct config_gen_ectx *config_gen_ectx,
	struct device_ectx *device_ectx,
	struct pipeline_ectx *pipeline_ectx,
	struct function_ectx *function_ectx,
	struct chain_ectx *chain_ectx
) {
	struct cp_config *cp_config = ADDR_OF(&cp_config_gen->cp_config);
	struct dp_config *dp_config = ADDR_OF(&cp_config->dp_config);
	struct memory_context *memory_context = &cp_config->memory_context;

	size_t ectx_size = sizeof(struct module_ectx);
	struct module_ectx *module_ectx =
		(struct module_ectx *)memory_balloc(memory_context, ectx_size);
	if (module_ectx == NULL) {
		NEW_ERROR(
			"failed to allocate memory for module execution context"
		);
		return NULL;
	}

	memset(module_ectx, 0, ectx_size);
	SET_OFFSET_OF(&module_ectx->cp_module, cp_module);

	SET_OFFSET_OF(&module_ectx->config_gen_ectx, config_gen_ectx);

	struct dp_module *dp_module =
		ADDR_OF(&dp_config->dp_modules) + cp_module->dp_module_idx;
	module_ectx->handler = dp_module->handler;

	struct cp_device *cp_device = ADDR_OF(&device_ectx->cp_device);
	struct cp_pipeline *cp_pipeline = ADDR_OF(&pipeline_ectx->cp_pipeline);
	struct cp_function *cp_function = ADDR_OF(&function_ectx->cp_function);
	struct cp_chain *cp_chain = ADDR_OF(&chain_ectx->cp_chain);

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
	if (counter_storage == NULL) {
		NEW_ERROR(
			"failed to spawn counter storage for module '%s:%s'",
			cp_module->type,
			cp_module->name
		);
		goto error;
	}

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
		PUSH_ERROR(
			"failed to insert counter storage for module '%s:%s'",
			cp_module->type,
			cp_module->name
		);
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
	struct config_gen_ectx *config_gen_ectx,
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
	if (chain_ectx == NULL) {
		NEW_ERROR(
			"failed to allocate memory for chain execution context"
		);
		return NULL;
	}

	memset(chain_ectx, 0, ectx_size);
	SET_OFFSET_OF(&chain_ectx->cp_chain, cp_chain);
	chain_ectx->length = cp_chain->length;

	struct cp_device *cp_device = ADDR_OF(&device_ectx->cp_device);
	struct cp_pipeline *cp_pipeline = ADDR_OF(&pipeline_ectx->cp_pipeline);
	struct cp_function *cp_function = ADDR_OF(&function_ectx->cp_function);

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
	if (counter_storage == NULL) {
		NEW_ERROR(
			"failed to spawn counter storage for chain '%s'",
			cp_chain->name
		);
		goto error;
	}

	if (cp_config_counter_storage_registry_insert_chain(
		    &cp_config_gen->counter_storage_registry,
		    cp_device->name,
		    cp_pipeline->name,
		    cp_function->name,
		    cp_chain->name,
		    counter_storage
	    )) {
		PUSH_ERROR(
			"failed to insert counter storage for chain '%s'",
			cp_chain->name
		);
		goto error;
	}

	SET_OFFSET_OF(&chain_ectx->counter_storage, counter_storage);

	for (uint64_t idx = 0; idx < cp_chain->length; ++idx) {

		struct cp_module *cp_module = cp_config_gen_lookup_module(
			cp_config_gen,
			cp_chain->modules[idx].type,
			cp_chain->modules[idx].name
		);

		if (cp_module == NULL) {
			NEW_ERROR(
				"module '%s:%s' not found in chain '%s'",
				cp_chain->modules[idx].type,
				cp_chain->modules[idx].name,
				cp_chain->name
			);
			goto error;
		}

		struct module_ectx *module_ectx = module_ectx_create(
			cp_config_gen,
			cp_module,
			old_config_gen,
			config_gen_ectx,
			device_ectx,
			pipeline_ectx,
			function_ectx,
			chain_ectx
		);
		if (module_ectx == NULL) {
			PUSH_ERROR(
				"failed to create module execution context for "
				"module '%s:%s' in chain '%s'",
				cp_module->type,
				cp_module->name,
				cp_chain->name
			);
			goto error;
		}

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
	struct config_gen_ectx *config_gen_ectx,
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
	if (function_ectx == NULL) {
		NEW_ERROR("failed to allocate memory for function execution "
			  "context");
		return NULL;
	}

	memset(function_ectx, 0, ectx_size);
	SET_OFFSET_OF(&function_ectx->cp_function, cp_function);
	function_ectx->chain_map_size = weight_sum;

	struct chain_ectx **chains = (struct chain_ectx **)memory_balloc(
		memory_context,
		sizeof(struct chain_ectx *) * cp_function->chain_count
	);
	if (chains == NULL) {
		NEW_ERROR(
			"failed to allocate memory for chains array in "
			"function '%s'",
			cp_function->name
		);
		goto error;
	}
	memset(chains, 0, sizeof(struct chain_ectx *) * cp_function->chain_count
	);
	SET_OFFSET_OF(&function_ectx->chains, chains);
	function_ectx->chain_count = cp_function->chain_count;

	struct cp_device *cp_device = ADDR_OF(&device_ectx->cp_device);
	struct cp_pipeline *cp_pipeline = ADDR_OF(&pipeline_ectx->cp_pipeline);

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
	if (counter_storage == NULL) {
		NEW_ERROR(
			"failed to spawn counter storage for function '%s'",
			cp_function->name
		);
		goto error;
	}

	if (cp_config_counter_storage_registry_insert_function(
		    &cp_config_gen->counter_storage_registry,
		    cp_device->name,
		    cp_pipeline->name,
		    cp_function->name,
		    counter_storage
	    )) {
		PUSH_ERROR(
			"failed to insert counter storage for function '%s'",
			cp_function->name
		);
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
			config_gen_ectx,
			device_ectx,
			pipeline_ectx,
			function_ectx
		);
		if (chain_ectx == NULL) {
			PUSH_ERROR(
				"failed to create chain execution context for "
				"chain '%s' in function '%s'",
				cp_chain->name,
				cp_function->name
			);
			goto error;
		}
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
	struct config_gen_ectx *config_gen_ectx,
	struct device_ectx *device_ectx
) {
	struct cp_config *cp_config = ADDR_OF(&cp_config_gen->cp_config);
	struct memory_context *memory_context = &cp_config->memory_context;

	size_t ectx_size = sizeof(struct pipeline_ectx) +
			   sizeof(struct function_ectx *) * cp_pipeline->length;

	struct pipeline_ectx *pipeline_ectx = (struct pipeline_ectx *)
		memory_balloc(memory_context, ectx_size);
	if (pipeline_ectx == NULL) {
		NEW_ERROR("failed to allocate memory for pipeline execution "
			  "context");
		return NULL;
	}
	memset(pipeline_ectx, 0, ectx_size);
	SET_OFFSET_OF(&pipeline_ectx->cp_pipeline, cp_pipeline);
	pipeline_ectx->length = cp_pipeline->length;

	struct cp_device *cp_device = ADDR_OF(&device_ectx->cp_device);

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
	if (counter_storage == NULL) {
		NEW_ERROR(
			"failed to spawn counter storage for pipeline '%s'",
			cp_pipeline->name
		);
		goto error;
	}

	if (cp_config_counter_storage_registry_insert_pipeline(
		    &cp_config_gen->counter_storage_registry,
		    cp_device->name,
		    cp_pipeline->name,
		    counter_storage
	    )) {
		PUSH_ERROR(
			"failed to insert counter storage for pipeline '%s'",
			cp_pipeline->name
		);
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
			config_gen_ectx,
			device_ectx,
			pipeline_ectx
		);
		if (function_ectx == NULL) {
			PUSH_ERROR(
				"failed to create function execution context "
				"for function '%s' in pipeline '%s'",
				cp_function->name,
				cp_pipeline->name
			);
			goto error;
		}

		SET_OFFSET_OF(pipeline_ectx->functions + idx, function_ectx);
	}

	return pipeline_ectx;

error:
	pipeline_ectx_free(cp_config_gen, pipeline_ectx);

	return NULL;
}

static void
device_entry_ectx_free(
	struct cp_config_gen *cp_config_gen,
	struct device_entry_ectx *device_entry_ectx
) {
	struct cp_config *cp_config = ADDR_OF(&cp_config_gen->cp_config);
	struct memory_context *memory_context = &cp_config->memory_context;

	struct pipeline_ectx **pipelines =
		ADDR_OF(&device_entry_ectx->pipelines);
	if (pipelines != NULL) {
		for (uint64_t idx = 0; idx < device_entry_ectx->pipeline_count;
		     ++idx) {
			struct pipeline_ectx *pipeline_ectx =
				ADDR_OF(pipelines + idx);
			if (pipeline_ectx == NULL)
				continue;
			pipeline_ectx_free(cp_config_gen, pipeline_ectx);
		}
	}

	memory_bfree(
		memory_context,
		pipelines,
		sizeof(struct pipeline_ectx *) *
			device_entry_ectx->pipeline_count
	);

	size_t ectx_size = sizeof(struct device_entry_ectx) +
			   sizeof(struct pipeline_ectx *) *
				   device_entry_ectx->pipeline_map_size;

	memory_bfree(memory_context, device_entry_ectx, ectx_size);
}

static struct device_entry_ectx *
device_entry_ectx_create(
	struct cp_config_gen *new_config_gen,
	struct config_gen_ectx *config_gen_ectx,
	struct device_ectx *device_ectx,
	device_handler handler,
	struct cp_device_entry *cp_device_entry,
	struct cp_config_gen *old_config_gen
) {
	struct cp_config *cp_config = ADDR_OF(&new_config_gen->cp_config);
	struct memory_context *memory_context = &cp_config->memory_context;

	uint64_t weight_sum = 0;
	for (uint64_t idx = 0; idx < cp_device_entry->pipeline_count; ++idx) {
		weight_sum += cp_device_entry->pipelines[idx].weight;
	}

	size_t ectx_size = sizeof(struct device_entry_ectx) +
			   sizeof(struct pipeline_ectx *) * weight_sum;

	struct device_entry_ectx *device_entry_ectx =
		(struct device_entry_ectx *)memory_balloc(
			memory_context, ectx_size
		);
	if (device_entry_ectx == NULL) {
		NEW_ERROR("failed to allocate memory for device entry "
			  "execution context");
		return NULL;
	}

	memset(device_entry_ectx, 0, ectx_size);
	device_entry_ectx->handler = handler;

	device_entry_ectx->pipeline_count = cp_device_entry->pipeline_count;
	if (!device_entry_ectx->pipeline_count)
		return device_entry_ectx;
	struct pipeline_ectx **pipelines =
		(struct pipeline_ectx **)memory_balloc(
			memory_context,
			sizeof(struct pipeline_ectx *) *
				device_entry_ectx->pipeline_count
		);
	if (pipelines == NULL) {
		NEW_ERROR("failed to allocate memory for pipelines array in "
			  "device entry");
		goto error;
	}
	memset(pipelines,
	       0,
	       sizeof(struct pipeline_ectx *) *
		       device_entry_ectx->pipeline_count);
	SET_OFFSET_OF(&device_entry_ectx->pipelines, pipelines);

	device_entry_ectx->pipeline_map_size = weight_sum;
	uint64_t pos = 0;
	for (uint64_t idx = 0; idx < cp_device_entry->pipeline_count; ++idx) {
		struct cp_pipeline *cp_pipeline = cp_config_gen_lookup_pipeline(
			new_config_gen, cp_device_entry->pipelines[idx].name
		);
		if (cp_pipeline == NULL) {
			NEW_ERROR(
				"pipeline '%s' not found in device entry",
				cp_device_entry->pipelines[idx].name
			);
			goto error;
		}
		struct pipeline_ectx *pipeline_ectx = pipeline_ectx_create(
			new_config_gen,
			cp_pipeline,
			old_config_gen,
			config_gen_ectx,
			device_ectx
		);
		if (pipeline_ectx == NULL) {
			PUSH_ERROR(
				"failed to create pipeline execution context "
				"for pipeline '%s' in device entry",
				cp_pipeline->name
			);
			goto error;
		}

		SET_OFFSET_OF(pipelines + idx, pipeline_ectx);

		for (uint64_t weight_idx = 0;
		     weight_idx < cp_device_entry->pipelines[idx].weight;
		     ++weight_idx) {
			SET_OFFSET_OF(
				device_entry_ectx->pipeline_map + pos,
				pipeline_ectx
			);
			++pos;
		}
	}

	return device_entry_ectx;

error:
	device_entry_ectx_free(new_config_gen, device_entry_ectx);
	return NULL;
}

static void
device_ectx_free(
	struct cp_config_gen *cp_config_gen, struct device_ectx *device_ectx
) {
	struct cp_config *cp_config = ADDR_OF(&cp_config_gen->cp_config);
	struct memory_context *memory_context = &cp_config->memory_context;

	struct device_entry_ectx *input =
		ADDR_OF(&device_ectx->input_pipelines);
	if (input)
		device_entry_ectx_free(cp_config_gen, input);
	struct device_entry_ectx *output =
		ADDR_OF(&device_ectx->output_pipelines);
	if (output)
		device_entry_ectx_free(cp_config_gen, output);

	struct counter_storage *counter_storage =
		ADDR_OF(&device_ectx->counter_storage);
	if (counter_storage != NULL)
		counter_storage_free(counter_storage);

	size_t ectx_size = sizeof(struct device_ectx);
	memory_bfree(memory_context, device_ectx, ectx_size);
}

static struct device_ectx *
device_ectx_create(
	struct cp_config_gen *cp_config_gen,
	struct cp_device *cp_device,
	struct config_gen_ectx *config_gen_ectx,
	struct cp_config_gen *old_config_gen
) {
	struct cp_config *cp_config = ADDR_OF(&cp_config_gen->cp_config);
	struct dp_config *dp_config = ADDR_OF(&cp_config->dp_config);
	struct memory_context *memory_context = &cp_config->memory_context;

	size_t ectx_size = sizeof(struct device_ectx);

	struct device_ectx *device_ectx =
		(struct device_ectx *)memory_balloc(memory_context, ectx_size);
	if (device_ectx == NULL) {
		NEW_ERROR(
			"failed to allocate memory for device execution context"
		);
		return NULL;
	}

	memset(device_ectx, 0, ectx_size);
	SET_OFFSET_OF(&device_ectx->cp_device, cp_device);

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
	if (counter_storage == NULL) {
		NEW_ERROR(
			"failed to spawn counter storage for device '%s'",
			cp_device->name
		);
		goto error;
	}

	if (cp_config_counter_storage_registry_insert_device(
		    &cp_config_gen->counter_storage_registry,
		    cp_device->name,
		    counter_storage
	    )) {
		PUSH_ERROR(
			"failed to insert counter storage for device '%s'",
			cp_device->name
		);
		goto error;
	}

	SET_OFFSET_OF(&device_ectx->counter_storage, counter_storage);

	struct dp_device *dp_device =
		ADDR_OF(&dp_config->dp_devices) + cp_device->dp_device_idx;

	struct device_entry_ectx *input = device_entry_ectx_create(
		cp_config_gen,
		config_gen_ectx,
		device_ectx,
		dp_device->input_handler,
		ADDR_OF(&cp_device->input_pipelines),
		old_config_gen
	);
	if (input == NULL) {
		PUSH_ERROR(
			"failed to create input device entry execution context "
			"for device '%s'",
			cp_device->name
		);
		goto error;
	}
	SET_OFFSET_OF(&device_ectx->input_pipelines, input);

	struct device_entry_ectx *output = device_entry_ectx_create(
		cp_config_gen,
		config_gen_ectx,
		device_ectx,
		dp_device->output_handler,
		ADDR_OF(&cp_device->output_pipelines),
		old_config_gen
	);
	if (output == NULL) {
		PUSH_ERROR(
			"failed to create output device entry execution "
			"context for device '%s'",
			cp_device->name
		);
		goto error;
	}
	SET_OFFSET_OF(&device_ectx->output_pipelines, output);

	return device_ectx;

error:
	device_ectx_free(cp_config_gen, device_ectx);

	return NULL;
}

void
config_gen_ectx_free(
	struct cp_config_gen *cp_config_gen,
	struct config_gen_ectx *config_gen_ectx
) {
	struct cp_config *cp_config = ADDR_OF(&cp_config_gen->cp_config);
	struct memory_context *memory_context = &cp_config->memory_context;

	for (uint64_t device_idx = 0;
	     device_idx < config_gen_ectx->device_count;
	     ++device_idx) {

		struct device_ectx *device_ectx =
			ADDR_OF(config_gen_ectx->devices + device_idx);

		if (device_ectx != NULL) {
			device_ectx_free(cp_config_gen, device_ectx);
		}
	}

	size_t ectx_size =
		sizeof(struct config_gen_ectx) +
		sizeof(struct device_ectx *) * config_gen_ectx->device_count;

	memory_bfree(memory_context, config_gen_ectx, ectx_size);
}

static int
link_module_ectx(
	struct config_gen_ectx *config_gen_ectx,
	struct device_ectx *device_ectx,
	struct device_entry_ectx *device_entry_ectx,
	struct pipeline_ectx *pipeline_ectx,
	struct function_ectx *function_ectx,
	struct chain_ectx *chain_ectx,
	struct module_ectx *module_ectx
) {
	(void)device_ectx;
	(void)device_entry_ectx;
	(void)pipeline_ectx;
	(void)function_ectx;
	(void)chain_ectx;

	struct cp_config_gen *cp_config_gen =
		ADDR_OF(&config_gen_ectx->cp_config_gen);
	struct cp_config *cp_config = ADDR_OF(&cp_config_gen->cp_config);
	struct memory_context *memory_context = &cp_config->memory_context;

	struct cp_module *cp_module = ADDR_OF(&module_ectx->cp_module);

	uint64_t *cm_index = (uint64_t *)memory_balloc(
		memory_context, sizeof(uint64_t) * config_gen_ectx->device_count
	);
	if (config_gen_ectx->device_count && cm_index == NULL) {
		NEW_ERROR(
			"failed to allocate memory for cm_index in module "
			"'%s:%s'",
			cp_module->type,
			cp_module->name
		);
		goto error;
	}
	for (uint64_t idx = 0; idx < config_gen_ectx->device_count; ++idx)
		cm_index[idx] = 0;
	SET_OFFSET_OF(&module_ectx->cm_index, cm_index);
	module_ectx->cm_index_size = config_gen_ectx->device_count;

	uint64_t *mc_index = (uint64_t *)memory_balloc(
		memory_context, sizeof(uint64_t) * cp_module->device_count
	);
	if (cp_module->device_count && mc_index == NULL) {
		NEW_ERROR(
			"failed to allocate memory for mc_index in module "
			"'%s:%s'",
			cp_module->type,
			cp_module->name
		);
		goto error;
	}
	for (uint64_t idx = 0; idx < cp_module->device_count; ++idx)
		mc_index[idx] = -1;
	SET_OFFSET_OF(&module_ectx->mc_index, mc_index);
	module_ectx->mc_index_size = cp_module->device_count;

	struct cp_module_device *m_devices = ADDR_OF(&cp_module->devices);

	for (uint64_t m_idx = 0; m_idx < cp_module->device_count; ++m_idx) {
		for (uint64_t c_idx = 0; c_idx < config_gen_ectx->device_count;
		     ++c_idx) {
			struct device_ectx *device_ectx =
				ADDR_OF(config_gen_ectx->devices + c_idx);
			if (device_ectx == NULL)
				continue;
			struct cp_device *cp_device =
				ADDR_OF(&device_ectx->cp_device);
			if (!strncmp(
				    m_devices[m_idx].name,
				    cp_device->name,
				    CP_DEVICE_NAME_LEN
			    )) {
				mc_index[m_idx] = c_idx;
				cm_index[c_idx] = m_idx;
			}
		}
	}

	return 0;

error:
	return -1;
}

static int
link_chain_ectx(
	struct config_gen_ectx *config_gen_ectx,
	struct device_ectx *device_ectx,
	struct device_entry_ectx *device_entry_ectx,
	struct pipeline_ectx *pipeline_ectx,
	struct function_ectx *function_ectx,
	struct chain_ectx *chain_ectx
) {
	for (uint64_t idx = 0; idx < chain_ectx->length; ++idx) {
		struct module_ectx *module_ectx =
			ADDR_OF(chain_ectx->modules + idx);
		if (module_ectx == NULL)
			continue;
		if (link_module_ectx(
			    config_gen_ectx,
			    device_ectx,
			    device_entry_ectx,
			    pipeline_ectx,
			    function_ectx,
			    chain_ectx,
			    module_ectx
		    )) {
			PUSH_ERROR("failed to link module execution context");
			goto error;
		}
	}

	return 0;

error:
	return -1;
}

static int
link_function_ectx(
	struct config_gen_ectx *config_gen_ectx,
	struct device_ectx *device_ectx,
	struct device_entry_ectx *device_entry_ectx,
	struct pipeline_ectx *pipeline_ectx,
	struct function_ectx *function_ectx
) {
	struct chain_ectx **chains = ADDR_OF(&function_ectx->chains);
	for (uint64_t idx = 0; idx < function_ectx->chain_count; ++idx) {
		struct chain_ectx *chain_ectx = ADDR_OF(chains + idx);
		if (chain_ectx == NULL)
			continue;
		if (link_chain_ectx(
			    config_gen_ectx,
			    device_ectx,
			    device_entry_ectx,
			    pipeline_ectx,
			    function_ectx,
			    chain_ectx
		    )) {
			PUSH_ERROR("failed to link chain execution context");
			goto error;
		}
	}

	return 0;

error:
	return -1;
}

static int
link_pipeline_ectx(
	struct config_gen_ectx *config_gen_ectx,
	struct device_ectx *device_ectx,
	struct device_entry_ectx *device_entry_ectx,
	struct pipeline_ectx *pipeline_ectx
) {
	for (uint64_t idx = 0; idx < pipeline_ectx->length; ++idx) {
		struct function_ectx *function_ectx =
			ADDR_OF(pipeline_ectx->functions + idx);
		if (function_ectx == NULL)
			continue;
		if (link_function_ectx(
			    config_gen_ectx,
			    device_ectx,
			    device_entry_ectx,
			    pipeline_ectx,
			    function_ectx
		    )) {
			PUSH_ERROR("failed to link function execution context");
			goto error;
		}
	}

	return 0;

error:
	return -1;
}

static int
link_device_entry_ectx(
	struct config_gen_ectx *config_gen_ectx,
	struct device_ectx *device_ectx,
	struct device_entry_ectx *device_entry_ectx
) {
	struct pipeline_ectx **pipelines =
		ADDR_OF(&device_entry_ectx->pipelines);
	for (uint64_t idx = 0; idx < device_entry_ectx->pipeline_count; ++idx) {
		struct pipeline_ectx *pipeline_ectx = ADDR_OF(pipelines + idx);
		if (pipeline_ectx == NULL)
			continue;
		if (link_pipeline_ectx(
			    config_gen_ectx,
			    device_ectx,
			    device_entry_ectx,
			    pipeline_ectx
		    )) {
			PUSH_ERROR("failed to link pipeline execution context");
			goto error;
		}
	}

	return 0;

error:
	return -1;
}

static int
link_device_ectx(
	struct config_gen_ectx *config_gen_ectx, struct device_ectx *device_ectx
) {
	if (link_device_entry_ectx(
		    config_gen_ectx,
		    device_ectx,
		    ADDR_OF(&device_ectx->input_pipelines)
	    )) {
		PUSH_ERROR("failed to link input device entry execution context"
		);
		goto error;
	}
	if (link_device_entry_ectx(
		    config_gen_ectx,
		    device_ectx,
		    ADDR_OF(&device_ectx->output_pipelines)
	    )) {
		PUSH_ERROR(
			"failed to link output device entry execution context"
		);
		goto error;
	}

	return 0;

error:
	return -1;
}

static int
link_config_gen_ectx(struct config_gen_ectx *config_gen_ectx) {
	for (uint64_t idx = 0; idx < config_gen_ectx->device_count; ++idx) {
		struct device_ectx *device_ectx =
			ADDR_OF(config_gen_ectx->devices + idx);
		if (device_ectx == NULL)
			continue;
		if (link_device_ectx(config_gen_ectx, device_ectx)) {
			PUSH_ERROR("failed to link device execution context");
			goto error;
		}
	}

	return 0;

error:
	return -1;
}

struct config_gen_ectx *
config_gen_ectx_create(
	struct cp_config_gen *cp_config_gen,
	struct cp_config_gen *old_config_gen
) {
	struct cp_config *cp_config = ADDR_OF(&cp_config_gen->cp_config);
	struct memory_context *memory_context = &cp_config->memory_context;

	size_t ectx_size = sizeof(struct config_gen_ectx) +
			   sizeof(struct device_ectx *) *
				   cp_device_registry_capacity(
					   &cp_config_gen->device_registry
				   );

	struct config_gen_ectx *config_gen_ectx = (struct config_gen_ectx *)
		memory_balloc(memory_context, ectx_size);
	if (config_gen_ectx == NULL) {
		NEW_ERROR("failed to allocate memory for config generation "
			  "execution context");
		return NULL;
	}
	memset(config_gen_ectx, 0, ectx_size);

	SET_OFFSET_OF(&config_gen_ectx->cp_config_gen, cp_config_gen);

	config_gen_ectx->device_count =
		cp_device_registry_capacity(&cp_config_gen->device_registry);

	for (uint64_t device_idx = 0;
	     // FIXME: cp_config_gen_device_count
	     device_idx <
	     cp_device_registry_capacity(&cp_config_gen->device_registry);
	     ++device_idx) {

		struct cp_device *cp_device =
			cp_config_gen_get_device(cp_config_gen, device_idx);
		if (cp_device == NULL) {
			config_gen_ectx->devices[device_idx] = NULL;
			continue;
		}

		struct device_ectx *device_ectx = device_ectx_create(
			cp_config_gen,
			cp_device,
			config_gen_ectx,
			old_config_gen
		);
		if (device_ectx == NULL) {
			PUSH_ERROR("failed to create device execution context");
			goto error;
		}
		SET_OFFSET_OF(
			config_gen_ectx->devices + device_idx, device_ectx
		);
	}

	if (link_config_gen_ectx(config_gen_ectx)) {
		PUSH_ERROR("failed to link config generation execution context"
		);
		goto error;
	}

	return config_gen_ectx;

error:
	config_gen_ectx_free(cp_config_gen, config_gen_ectx);

	return NULL;
}
