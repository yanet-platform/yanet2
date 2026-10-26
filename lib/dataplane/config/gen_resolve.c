#include "gen_resolve.h"

#include "lib/dataplane/pipeline/econtext.h"

#include "lib/controlplane/config/zone.h"
#include "lib/dataplane/config/zone.h"
#include "lib/logging/log.h"

struct dp_config *
module_ectx_dp_config(struct module_ectx *module_ectx) {
	struct config_gen_ectx *config_gen_ectx =
		module_ectx->abs_config_gen_ectx;
	if (config_gen_ectx == NULL) {
		return NULL;
	}

	struct cp_config_gen *cp_config_gen =
		ADDR_OF(&config_gen_ectx->cp_config_gen);
	if (cp_config_gen == NULL) {
		return NULL;
	}

	return ADDR_OF(&cp_config_gen->dp_config);
}

static void
module_ectx_resolve_absolutes(struct module_ectx *module_ectx) {
	struct cp_module *cp_module = ADDR_OF(&module_ectx->cp_module);
	struct counter_storage *counter_storage =
		ADDR_OF(&module_ectx->counter_storage);

	module_ectx->abs_cp_module = cp_module;

	module_ectx->rx_counter = counter_get_value_handle(
		cp_module->rx_counter_id, counter_storage
	);
	module_ectx->tx_counter = counter_get_value_handle(
		cp_module->tx_counter_id, counter_storage
	);
	module_ectx->drop_counter = counter_get_value_handle(
		cp_module->drop_counter_id, counter_storage
	);
	module_ectx->pending_input_counter = counter_get_value_handle(
		cp_module->pending_input_counter_id, counter_storage
	);
	module_ectx->pending_output_counter = counter_get_value_handle(
		cp_module->pending_output_counter_id, counter_storage
	);

	struct module_device_target *device_targets =
		ADDR_OF(&module_ectx->device_targets);
	module_ectx->abs_device_targets = device_targets;
	if (device_targets != NULL) {
		for (uint64_t idx = 0; idx < module_ectx->device_target_count;
		     ++idx) {
			device_targets[idx].abs_input_entry =
				ADDR_OF(&device_targets[idx].input_entry);
			device_targets[idx].abs_output_entry =
				ADDR_OF(&device_targets[idx].output_entry);
		}
	}

	module_ectx->abs_counter_storage =
		ADDR_OF(&module_ectx->counter_storage);
	module_ectx->abs_config_gen_ectx =
		ADDR_OF(&module_ectx->config_gen_ectx);

	struct counter_storage **abs_runtime =
		ADDR_OF(&module_ectx->abs_runtime_counter_storages);
	module_ectx->abs_runtime_counter_storages_base = abs_runtime;
	if (abs_runtime != NULL) {
		struct counter_storage **runtime =
			ADDR_OF(&module_ectx->runtime_counter_storages);
		for (uint64_t idx = 0;
		     idx < module_ectx->runtime_counter_storage_count;
		     ++idx) {
			abs_runtime[idx] = ADDR_OF(runtime + idx);
		}
	}

	struct module_object_link_ectx *object_links =
		ADDR_OF(&module_ectx->object_links);
	module_ectx->abs_object_links = object_links;
	for (uint64_t idx = 0; idx < module_ectx->object_link_count; ++idx) {
		object_links[idx].abs_object_ectx =
			ADDR_OF(&object_links[idx].object_ectx);
	}

	module_ectx->abs_module_prepared =
		ADDR_OF(&module_ectx->module_prepared);
	if (module_ectx->abs_module_prepared == NULL) {
		return;
	}

	// Filling the private buffer joins the derivations above: the
	// single-writer pass re-derives from authoritative fields, so a
	// repeated pass rewrites the same values. The skips are defensive
	// only — the slot index is validated when the config is built.
	struct dp_config *dp_config = module_ectx_dp_config(module_ectx);
	if (dp_config == NULL) {
		LOG(ERROR,
		    "module context commit skipped for '%s:%s': no dataplane "
		    "config",
		    cp_module->type,
		    cp_module->name);
		return;
	}
	if (cp_module->dp_module_idx >= dp_config->module_count) {
		LOG(ERROR,
		    "module context commit skipped for '%s:%s': no dataplane "
		    "module",
		    cp_module->type,
		    cp_module->name);
		return;
	}
	struct dp_module *dp_module =
		ADDR_OF(&dp_config->dp_modules) + cp_module->dp_module_idx;
	if (dp_module->commit_ectx_handler != NULL) {
		dp_module->commit_ectx_handler(module_ectx, cp_module);
	}
}

// Derive an object context's absolute addresses and fill its private
// per-context buffer through the object type's registered hook, the
// object twin of the module pass above. The dataplane config is reached
// through the owning generation instead of a back reference: unlike a
// module context, an object context is never dereferenced from the
// packet hot path to find its config.
static void
object_ectx_resolve_absolutes(
	struct object_ectx *object_ectx, struct config_gen_ectx *config_gen_ectx
) {
	struct cp_object *cp_object = ADDR_OF(&object_ectx->cp_object);
	object_ectx->abs_cp_object = cp_object;

	object_ectx->abs_object_prepared =
		ADDR_OF(&object_ectx->object_prepared);
	if (object_ectx->abs_object_prepared == NULL) {
		return;
	}

	// Filling the private buffer joins the derivations above: the
	// single-writer pass re-derives from authoritative fields, so a
	// repeated pass rewrites the same values. The skips are defensive
	// only — the slot index is validated when the config is built.
	struct cp_config_gen *cp_config_gen =
		ADDR_OF(&config_gen_ectx->cp_config_gen);
	struct dp_config *dp_config = ADDR_OF(&cp_config_gen->dp_config);
	if (dp_config == NULL) {
		LOG(ERROR,
		    "object context commit skipped for '%s:%s': no dataplane "
		    "config",
		    cp_object->type,
		    cp_object->name);
		return;
	}
	if (cp_object->dp_object_idx >= dp_config->object_count) {
		LOG(ERROR,
		    "object context commit skipped for '%s:%s': no dataplane "
		    "object",
		    cp_object->type,
		    cp_object->name);
		return;
	}
	struct dp_object *dp_object =
		ADDR_OF(&dp_config->dp_objects) + cp_object->dp_object_idx;
	if (dp_object->commit_ectx_handler != NULL) {
		dp_object->commit_ectx_handler(object_ectx, cp_object);
	}
}

static void
chain_ectx_resolve_absolutes(
	struct chain_ectx *chain_ectx, struct config_gen_ectx *config_gen_ectx
) {
	struct cp_chain *cp_chain = ADDR_OF(&chain_ectx->cp_chain);
	struct counter_storage *counter_storage =
		ADDR_OF(&chain_ectx->counter_storage);

	// Finished chain drop lists bypass the per-stage merges and land
	// directly in the final drop list of this worker's round.
	chain_ectx->abs_drop_sink = &config_gen_ectx->packet_front.drop;

	chain_ectx->abs_counter_packet_pending_input = counter_get_value_handle(
		cp_chain->counter_packet_pending_input, counter_storage
	);
	chain_ectx->abs_counter_packet_pending_output =
		counter_get_value_handle(
			cp_chain->counter_packet_pending_output, counter_storage
		);

	struct module_ectx **module_ptrs = ADDR_OF(&chain_ectx->module_ptrs);
	for (uint64_t idx = 0; idx < chain_ectx->length; ++idx) {
		struct module_ectx *module_ectx = ADDR_OF(module_ptrs + idx);
		chain_ectx->modules[idx] = module_ectx;
		module_ectx_resolve_absolutes(module_ectx);
	}
}

static void
function_ectx_resolve_absolutes(
	struct function_ectx *function_ectx,
	struct config_gen_ectx *config_gen_ectx
) {
	struct cp_function *cp_function = ADDR_OF(&function_ectx->cp_function);
	struct counter_storage *counter_storage =
		ADDR_OF(&function_ectx->counter_storage);

	function_ectx->abs_counter_packet_in = counter_get_value_handle(
		cp_function->counter_packet_in, counter_storage
	);
	function_ectx->abs_counter_packet_out = counter_get_value_handle(
		cp_function->counter_packet_out, counter_storage
	);
	function_ectx->abs_counter_packet_drop = counter_get_value_handle(
		cp_function->counter_packet_drop, counter_storage
	);
	function_ectx->abs_counter_packet_pending_input =
		counter_get_value_handle(
			cp_function->counter_packet_pending_input,
			counter_storage
		);
	function_ectx->abs_counter_packet_pending_output =
		counter_get_value_handle(
			cp_function->counter_packet_pending_output,
			counter_storage
		);

	struct chain_ectx **chain_ptrs = ADDR_OF(&function_ectx->chain_ptrs);
	struct chain_ectx **chains = ADDR_OF(&function_ectx->chains);
	function_ectx->abs_chains = chains;
	for (uint64_t idx = 0; idx < function_ectx->chain_count; ++idx) {
		chains[idx] = ADDR_OF(chain_ptrs + idx);
		chain_ectx_resolve_absolutes(chains[idx], config_gen_ectx);
	}

	// Recode the chain map to absolute addresses in place.
	//
	// The recode re-derives the weighted expansion from the relative
	// chain addresses and the controlplane weights, in the order
	// creation used, so a repeated pass never transforms an
	// already-absolute entry.
	uint64_t pos = 0;
	for (uint64_t idx = 0; idx < function_ectx->chain_count; ++idx) {
		for (uint64_t weight_idx = 0;
		     weight_idx < cp_function->chains[idx].weight;
		     ++weight_idx) {
			function_ectx->chain_map[pos] =
				ADDR_OF(chain_ptrs + idx);
			++pos;
		}
	}
}

static void
pipeline_ectx_resolve_absolutes(
	struct pipeline_ectx *pipeline_ectx,
	struct config_gen_ectx *config_gen_ectx
) {
	struct cp_pipeline *cp_pipeline = ADDR_OF(&pipeline_ectx->cp_pipeline);
	struct counter_storage *counter_storage =
		ADDR_OF(&pipeline_ectx->counter_storage);

	pipeline_ectx->abs_counter_packet_in = counter_get_value_handle(
		cp_pipeline->counter_packet_in, counter_storage
	);
	pipeline_ectx->abs_counter_packet_out = counter_get_value_handle(
		cp_pipeline->counter_packet_out, counter_storage
	);
	pipeline_ectx->abs_counter_packet_drop = counter_get_value_handle(
		cp_pipeline->counter_packet_drop, counter_storage
	);
	pipeline_ectx->abs_counter_packet_pending_input =
		counter_get_value_handle(
			cp_pipeline->counter_packet_pending_input,
			counter_storage
		);
	pipeline_ectx->abs_counter_packet_pending_output =
		counter_get_value_handle(
			cp_pipeline->counter_packet_pending_output,
			counter_storage
		);

	struct function_ectx **function_ptrs =
		ADDR_OF(&pipeline_ectx->function_ptrs);
	for (uint64_t idx = 0; idx < pipeline_ectx->length; ++idx) {
		pipeline_ectx->functions[idx] = ADDR_OF(function_ptrs + idx);
		function_ectx_resolve_absolutes(
			pipeline_ectx->functions[idx], config_gen_ectx
		);
	}
}

static void
device_entry_ectx_resolve_absolutes(
	struct device_entry_ectx *entry_ectx,
	struct cp_device_entry *cp_device_entry,
	struct counter_storage *counter_storage,
	struct config_gen_ectx *config_gen_ectx
) {
	entry_ectx->counter_packet_rx = counter_get_value_handle(
		cp_device_entry->counter_packet_rx, counter_storage
	);
	entry_ectx->counter_packet_entry = counter_get_value_handle(
		cp_device_entry->counter_packet_entry, counter_storage
	);
	entry_ectx->counter_packet_tx = counter_get_value_handle(
		cp_device_entry->counter_packet_tx, counter_storage
	);
	entry_ectx->counter_packet_drop = counter_get_value_handle(
		cp_device_entry->counter_packet_drop, counter_storage
	);
	entry_ectx->counter_packet_recirc_drop = counter_get_value_handle(
		cp_device_entry->counter_packet_recirc_drop, counter_storage
	);
	entry_ectx->counter_packet_pending_input = counter_get_value_handle(
		cp_device_entry->counter_packet_pending_input, counter_storage
	);
	entry_ectx->counter_packet_pending_output = counter_get_value_handle(
		cp_device_entry->counter_packet_pending_output, counter_storage
	);
	entry_ectx->abs_device_ectx = ADDR_OF(&entry_ectx->device_ectx);

	struct pipeline_ectx **pipeline_ptrs =
		ADDR_OF(&entry_ectx->pipeline_ptrs);
	struct pipeline_ectx **pipelines = ADDR_OF(&entry_ectx->pipelines);
	entry_ectx->abs_pipelines = pipelines;
	for (uint64_t idx = 0; idx < entry_ectx->pipeline_count; ++idx) {
		pipelines[idx] = ADDR_OF(pipeline_ptrs + idx);
		pipeline_ectx_resolve_absolutes(
			pipelines[idx], config_gen_ectx
		);
	}

	// Recode the pipeline map to absolute addresses in place.
	//
	// The recode re-derives the weighted expansion from the relative
	// pipeline addresses and the controlplane weights, in the order
	// creation used, so a repeated pass never transforms an
	// already-absolute entry.
	uint64_t pos = 0;
	for (uint64_t idx = 0; idx < entry_ectx->pipeline_count; ++idx) {
		for (uint64_t weight_idx = 0;
		     weight_idx < cp_device_entry->pipelines[idx].weight;
		     ++weight_idx) {
			entry_ectx->pipeline_map[pos] =
				ADDR_OF(pipeline_ptrs + idx);
			++pos;
		}
	}
}

void
config_gen_ectx_resolve_counters(struct config_gen_ectx *config_gen_ectx) {
	struct object_ectx **objects = ADDR_OF(&config_gen_ectx->objects);
	for (uint64_t idx = 0; idx < config_gen_ectx->object_count; ++idx) {
		struct object_ectx *object_ectx = ADDR_OF(objects + idx);
		if (object_ectx == NULL) {
			continue;
		}
		object_ectx_resolve_absolutes(object_ectx, config_gen_ectx);
	}

	struct device_ectx **device_ptrs =
		ADDR_OF(&config_gen_ectx->device_ptrs);
	config_gen_ectx->abs_egress_backing =
		ADDR_OF(&config_gen_ectx->egress_backing);
	config_gen_ectx->abs_tx_buckets = ADDR_OF(&config_gen_ectx->tx_buckets);
	config_gen_ectx->abs_backing_registry =
		ADDR_OF(&config_gen_ectx->backing_registry);
	for (uint64_t idx = 0; idx < config_gen_ectx->device_count; ++idx) {
		config_gen_ectx->devices[idx] = ADDR_OF(device_ptrs + idx);
		struct device_ectx *device_ectx = config_gen_ectx->devices[idx];
		if (device_ectx == NULL) {
			continue;
		}

		struct cp_device *cp_device = ADDR_OF(&device_ectx->cp_device);
		device_ectx->abs_cp_device = cp_device;
		struct counter_storage *counter_storage =
			ADDR_OF(&device_ectx->counter_storage);

		struct device_entry_ectx *input =
			ADDR_OF(&device_ectx->input_pipelines);
		device_ectx->abs_input_pipelines = input;
		if (input != NULL) {
			device_entry_ectx_resolve_absolutes(
				input,
				ADDR_OF(&cp_device->input_pipelines),
				counter_storage,
				config_gen_ectx
			);
		}

		struct device_entry_ectx *output =
			ADDR_OF(&device_ectx->output_pipelines);
		device_ectx->abs_output_pipelines = output;
		if (output != NULL) {
			device_entry_ectx_resolve_absolutes(
				output,
				ADDR_OF(&cp_device->output_pipelines),
				counter_storage,
				config_gen_ectx
			);
		}
	}
}
