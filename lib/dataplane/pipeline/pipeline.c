#include "pipeline.h"

#include "lib/dataplane/pipeline/econtext.h"

#include "lib/controlplane/config/zone.h"
#include "lib/dataplane/config/zone.h"
#include "lib/dataplane/module/packet_front.h"
#include "lib/dataplane/packet/packet.h"
#include "lib/logging/log.h"

// Packets and first-segment bytes currently crossing a stage boundary.
//
// The tally flows through the pipeline and function stages as a local:
// every boundary above the chains is either a per-packet loop that
// counts as it moves packets or a wholesale move whose tally the
// producer already knows, so no carrier list needs counters of its own.
struct packet_tally {
	uint64_t packets;
	uint64_t bytes;
};

static inline void
counter_add_packets_bytes(
	struct counter_value_handle *counter, uint64_t packets, uint64_t bytes
) {
	if (packets == 0) {
		return;
	}

	uint64_t *values = counter_handle_get_value(counter);
	values[0] += packets;
	values[1] += bytes;
}

static inline void
module_ectx_process(
	struct dp_worker *dp_worker,
	struct module_ectx *module_ectx,
	struct packet_front *packet_front

) {
	uint64_t drop_count = packet_front_drop_count(packet_front);
	uint64_t drop_bytes = packet_front_drop_bytes(packet_front);

	const size_t packets_count = packet_front_input_count(packet_front);

	const uint64_t input_bytes = packet_front_input_bytes(packet_front);
	counter_add_packets_bytes(
		module_ectx->rx_counter, packets_count, input_bytes
	);

	module_ectx->handler(dp_worker, module_ectx, packet_front);

	counter_add_packets_bytes(
		module_ectx->tx_counter,
		packet_front_output_count(packet_front),
		packet_front_output_bytes(packet_front)
	);
	counter_add_packets_bytes(
		module_ectx->drop_counter,
		packet_front_drop_count(packet_front) - drop_count,
		packet_front_drop_bytes(packet_front) - drop_bytes
	);
}

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
	module_ectx->abs_chain_ectx = ADDR_OF(&module_ectx->chain_ectx);

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

static void
chain_ectx_resolve_absolutes(
	struct chain_ectx *chain_ectx,
	struct config_gen_ectx *config_gen_ectx,
	struct function_ectx *function_ectx,
	struct pipeline_ectx *pipeline_ectx,
	struct device_entry_ectx *entry_ectx
) {
	struct cp_chain *cp_chain = ADDR_OF(&chain_ectx->cp_chain);
	struct counter_storage *counter_storage =
		ADDR_OF(&chain_ectx->counter_storage);

	// Finished chain drop lists bypass every carrier and land directly
	// in the final drop list of this worker's round.
	chain_ectx->abs_drop_sink = &config_gen_ectx->round_drop;

	chain_ectx->abs_counter_packet_pending_input = counter_get_value_handle(
		cp_chain->counter_packet_pending_input, counter_storage
	);
	chain_ectx->abs_counter_packet_pending_output =
		counter_get_value_handle(
			cp_chain->counter_packet_pending_output, counter_storage
		);

	// The enclosing stages resolve their own counters before walking
	// their chains, so every sink handle is in scope here.
	struct chain_ectx_sinks *sinks = &chain_ectx->sinks;
	sinks->function_drop = function_ectx->abs_counter_packet_drop;
	sinks->function_pending_input =
		function_ectx->abs_counter_packet_pending_input;
	sinks->function_pending_output =
		function_ectx->abs_counter_packet_pending_output;
	sinks->pipeline_drop = pipeline_ectx->abs_counter_packet_drop;
	sinks->pipeline_pending_input =
		pipeline_ectx->abs_counter_packet_pending_input;
	sinks->pipeline_pending_output =
		pipeline_ectx->abs_counter_packet_pending_output;
	sinks->entry_drop = entry_ectx->counter_packet_drop;
	sinks->entry_pending_input = entry_ectx->counter_packet_pending_input;
	sinks->entry_pending_output = entry_ectx->counter_packet_pending_output;

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
	struct config_gen_ectx *config_gen_ectx,
	struct pipeline_ectx *pipeline_ectx,
	struct device_entry_ectx *entry_ectx
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
		chain_ectx_resolve_absolutes(
			chains[idx],
			config_gen_ectx,
			function_ectx,
			pipeline_ectx,
			entry_ectx
		);
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
	struct config_gen_ectx *config_gen_ectx,
	struct device_entry_ectx *entry_ectx
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
			pipeline_ectx->functions[idx],
			config_gen_ectx,
			pipeline_ectx,
			entry_ectx
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
			pipelines[idx], config_gen_ectx, entry_ectx
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

// Derive the absolute addresses the packet hot path runs on: the
// counter pointers of every stage, resolved from the counter registry
// ids and the stage counter storages; the stage hop addresses, copied
// or recoded from the controlplane-owned relative arrays; every
// object's controlplane counterpart, copied from the generation's
// relative object array; and each module's private per-context
// buffer, filled by its execution-context commit handler when one is
// registered.
//
// Runs in the dataplane process before the context is released to the
// worker, and recomputes everything from controlplane-owned data, so a
// re-run never corrupts a previous derivation.
void
config_gen_ectx_resolve_counters(struct config_gen_ectx *config_gen_ectx) {
	struct object_ectx **objects = ADDR_OF(&config_gen_ectx->objects);
	for (uint64_t idx = 0; idx < config_gen_ectx->object_count; ++idx) {
		struct object_ectx *object_ectx = ADDR_OF(objects + idx);
		if (object_ectx == NULL) {
			continue;
		}
		object_ectx->abs_cp_object = ADDR_OF(&object_ectx->cp_object);
	}

	struct device_ectx **device_ptrs =
		ADDR_OF(&config_gen_ectx->device_ptrs);
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

static inline void
chain_ectx_process(
	struct dp_worker *dp_worker,
	struct chain_ectx *chain_ectx,
	struct packet_front *packet_front
) {
	// A chain with no modules is a wire: pass input through to output so
	// the caller's fold carries the packets onward.
	if (chain_ectx->length == 0) {
		packet_front_pass(packet_front);
		return;
	}

	for (uint64_t idx = 0; idx < chain_ectx->length; ++idx) {
		if (idx > 0) {
			packet_front_switch(packet_front);
		}

		module_ectx_process(
			dp_worker, chain_ectx->modules[idx], packet_front
		);
	}

	// A finished drop list is terminal: splice the packets straight into
	// the round's drop sink and credit the drop counters of every
	// enclosing stage directly, so neither the drops nor their tallies
	// travel the carriers upward.
	packet_list_concat(chain_ectx->abs_drop_sink, &packet_front->drop);

	struct chain_ectx_sinks *sinks = &chain_ectx->sinks;
	counter_add_packets_bytes(
		sinks->entry_drop,
		packet_front->drop_count,
		packet_front->drop_bytes
	);
	counter_add_packets_bytes(
		sinks->pipeline_drop,
		packet_front->drop_count,
		packet_front->drop_bytes
	);
	counter_add_packets_bytes(
		sinks->function_drop,
		packet_front->drop_count,
		packet_front->drop_bytes
	);
}

// Run one chain on its schedule front and fold the result into the
// function's carrier: the output list is concatenated onto the carrier
// and its tallies added to the running tally. Resets the schedule for
// its next use.
static inline void
chain_ectx_run(
	struct dp_worker *dp_worker,
	struct chain_ectx *chain_ectx,
	struct packet_list *carrier,
	struct packet_tally *tally
) {
	struct packet_front *schedule = &chain_ectx->schedule;

	chain_ectx_process(dp_worker, chain_ectx, schedule);

	tally->packets += schedule->output_count;
	tally->bytes += schedule->output_bytes;
	packet_list_concat(carrier, &schedule->output);

	packet_front_init(schedule);
}

// Run the function's only chain.
//
// With a single chain every packet maps to chain 0, so the per-packet
// hash demux is skipped: the carrier moves into the chain front in one
// step and the entry tally becomes the chain's input tally wholesale.
static inline void
function_ectx_run_single_chain(
	struct dp_worker *dp_worker,
	struct function_ectx *function_ectx,
	struct packet_list *carrier,
	struct packet_tally *tally
) {
	struct chain_ectx *chain_ectx = function_ectx->abs_chains[0];
	struct packet_front *schedule = &chain_ectx->schedule;

	schedule->input_count = tally->packets;
	schedule->input_bytes = tally->bytes;
	packet_list_concat(&schedule->input, carrier);

	*tally = (struct packet_tally){0, 0};

	chain_ectx_run(dp_worker, chain_ectx, carrier, tally);
}

// Demultiplex the carrier across the function's chains by hash.
//
// Each chain runs on its own front — the one place tallies are kept —
// and the outputs fold back into the carrier.
static inline void
function_ectx_run_chains(
	struct dp_worker *dp_worker,
	struct function_ectx *function_ectx,
	struct packet_list *carrier,
	struct packet_tally *tally
) {
	uint64_t map_size = function_ectx->chain_map_size;

	struct packet *packet = packet_list_pop(carrier);
	while (packet != NULL) {
		uint64_t map_idx = ((uint64_t)packet->hash * map_size) >> 32;

		struct chain_ectx *chain_ectx =
			function_ectx->chain_map[map_idx];
		packet_front_input(&chain_ectx->schedule, packet);

		packet = packet_list_pop(carrier);
	}

	*tally = (struct packet_tally){0, 0};

	struct chain_ectx **chains = function_ectx->abs_chains;
	for (uint64_t idx = 0; idx < function_ectx->chain_count; ++idx) {
		chain_ectx_run(dp_worker, chains[idx], carrier, tally);
	}
}

// Drain a function whose chains are all zero-weight (fully disabled).
//
// There is no chain to route packets to, so the output is dropped and
// the drop counters of the function and its enclosing stages are
// credited directly. The chains are still run on empty fronts so the
// worker keeps force-polling every module once per tick for periodic
// work, exactly as the demux path did for a function with no packets
// to route.
static inline void
function_ectx_drain(
	struct dp_worker *dp_worker,
	struct function_ectx *function_ectx,
	struct pipeline_ectx *pipeline_ectx,
	struct device_entry_ectx *entry_ectx,
	struct config_gen_ectx *config_gen_ectx,
	struct packet_list *carrier,
	struct packet_tally *tally
) {
	packet_list_concat(&config_gen_ectx->round_drop, carrier);

	counter_add_packets_bytes(
		function_ectx->abs_counter_packet_drop,
		tally->packets,
		tally->bytes
	);
	counter_add_packets_bytes(
		pipeline_ectx->abs_counter_packet_drop,
		tally->packets,
		tally->bytes
	);
	counter_add_packets_bytes(
		entry_ectx->counter_packet_drop, tally->packets, tally->bytes
	);

	*tally = (struct packet_tally){0, 0};

	struct chain_ectx **chains = function_ectx->abs_chains;
	for (uint64_t idx = 0; idx < function_ectx->chain_count; ++idx) {
		chain_ectx_run(dp_worker, chains[idx], carrier, tally);
	}
}

static inline void
function_ectx_process(
	struct dp_worker *dp_worker,
	struct function_ectx *function_ectx,
	struct pipeline_ectx *pipeline_ectx,
	struct device_entry_ectx *entry_ectx,
	struct config_gen_ectx *config_gen_ectx,
	struct packet_list *carrier,
	struct packet_tally *tally
) {
	counter_add_packets_bytes(
		function_ectx->abs_counter_packet_in,
		tally->packets,
		tally->bytes
	);

	if (function_ectx->chain_map_size == 0) {
		function_ectx_drain(
			dp_worker,
			function_ectx,
			pipeline_ectx,
			entry_ectx,
			config_gen_ectx,
			carrier,
			tally
		);
	} else if (function_ectx->chain_count == 1) {
		function_ectx_run_single_chain(
			dp_worker, function_ectx, carrier, tally
		);
	} else {
		function_ectx_run_chains(
			dp_worker, function_ectx, carrier, tally
		);
	}

	counter_add_packets_bytes(
		function_ectx->abs_counter_packet_out,
		tally->packets,
		tally->bytes
	);
}

// Process one pipeline on its bare carrier.
//
// in holds the tally of the packets dispatch placed on the carrier; it
// flows through the functions as the running output tally, so each
// function's in-counter is the previous stage's out by construction.
// The final tally credits the pipeline's out-counter and the entry's
// tx-counter, and the carrier's packets move back onto the entry front:
// the entry level owns the direction policy, dropping the output of an
// input entry instead of letting it transmit.
static inline void
pipeline_ectx_process(
	struct dp_worker *dp_worker,
	struct pipeline_ectx *pipeline_ectx,
	struct device_entry_ectx *entry_ectx,
	struct config_gen_ectx *config_gen_ectx,
	struct packet_front *packet_front,
	struct packet_list *carrier,
	const struct packet_tally *in
) {
	struct packet_tally tally = *in;

	for (uint64_t idx = 0; idx < pipeline_ectx->length; ++idx) {
		function_ectx_process(
			dp_worker,
			pipeline_ectx->functions[idx],
			pipeline_ectx,
			entry_ectx,
			config_gen_ectx,
			carrier,
			&tally
		);
	}

	counter_add_packets_bytes(
		pipeline_ectx->abs_counter_packet_out,
		tally.packets,
		tally.bytes
	);
	counter_add_packets_bytes(
		entry_ectx->counter_packet_tx, tally.packets, tally.bytes
	);

	packet_list_concat(&packet_front->output, carrier);
	packet_front->output_count += tally.packets;
	packet_front->output_bytes += tally.bytes;
}

// Run the entry's only pipeline.
//
// With a single pipeline every packet maps to pipeline 0, so the
// per-packet hash demux is skipped: the handler output moves to the
// pipeline's carrier in one step and the pipeline's in-counter is
// credited wholesale from the front's output tally.
static inline void
device_entry_ectx_dispatch_single(
	struct dp_worker *dp_worker,
	struct device_entry_ectx *entry_ectx,
	struct config_gen_ectx *config_gen_ectx,
	struct packet_front *packet_front
) {
	struct pipeline_ectx *pipeline_ectx = entry_ectx->abs_pipelines[0];

	struct packet_tally in = {
		packet_front->output_count,
		packet_front->output_bytes,
	};

	counter_add_packets_bytes(
		pipeline_ectx->abs_counter_packet_in, in.packets, in.bytes
	);
	packet_list_concat(&pipeline_ectx->schedule, &packet_front->output);
	packet_front->output_count = 0;
	packet_front->output_bytes = 0;

	pipeline_ectx_process(
		dp_worker,
		pipeline_ectx,
		entry_ectx,
		config_gen_ectx,
		packet_front,
		&pipeline_ectx->schedule,
		&in
	);
}

// Demultiplex the handler output across the entry's pipelines by hash.
//
// The demux loop is the one place each pipeline's in-counter is
// credited: it already touches every packet. Each pipeline then works
// its own carrier.
static inline void
device_entry_ectx_dispatch_many(
	struct dp_worker *dp_worker,
	struct device_entry_ectx *entry_ectx,
	struct config_gen_ectx *config_gen_ectx,
	struct packet_front *packet_front
) {
	struct pipeline_ectx **pipelines = entry_ectx->abs_pipelines;

	struct pipeline_ectx **pipeline_map = entry_ectx->pipeline_map;
	uint64_t map_size = entry_ectx->pipeline_map_size;

	struct packet *packet = packet_list_pop(&packet_front->output);
	while (packet != NULL) {
		uint64_t map_idx = ((uint64_t)packet->hash * map_size) >> 32;
		struct pipeline_ectx *pipeline_ectx = pipeline_map[map_idx];

		uint64_t *values = counter_handle_get_value(
			pipeline_ectx->abs_counter_packet_in
		);
		values[0] += 1;
		values[1] += packet->data_len;

		packet_list_add(&pipeline_ectx->schedule, packet);

		packet = packet_list_pop(&packet_front->output);
	}
	packet_front->output_count = 0;
	packet_front->output_bytes = 0;

	for (uint64_t idx = 0; idx < entry_ectx->pipeline_count; ++idx) {
		struct pipeline_ectx *pipeline_ectx = pipelines[idx];

		struct packet_tally in = {0, 0};
		pipeline_ectx_process(
			dp_worker,
			pipeline_ectx,
			entry_ectx,
			config_gen_ectx,
			packet_front,
			&pipeline_ectx->schedule,
			&in
		);
	}
}

// Drain a device entry that has no routable pipeline.
//
// The unroutable output is dropped and the entry's drop counter is
// credited from the front's output tally. If the entry has pipelines
// but they are all zero-weight, they are still worked on empty carriers
// so the worker keeps force-polling every module once per tick for
// periodic work; reusing the demux is safe once the output list is
// empty, since its per-packet loop never runs and the zero-sized
// pipeline map is never indexed. An entry with no pipelines at all has
// nothing to poll, so the demux — which would size a zero-length
// scheduling array — is skipped.
static inline void
device_entry_ectx_drain(
	struct dp_worker *dp_worker,
	struct device_entry_ectx *entry_ectx,
	struct config_gen_ectx *config_gen_ectx,
	struct packet_front *packet_front
) {
	counter_add_packets_bytes(
		entry_ectx->counter_packet_drop,
		packet_front->output_count,
		packet_front->output_bytes
	);
	packet_list_concat(&config_gen_ectx->round_drop, &packet_front->output);
	packet_front->output_count = 0;
	packet_front->output_bytes = 0;

	if (entry_ectx->pipeline_count > 0) {
		device_entry_ectx_dispatch_many(
			dp_worker, entry_ectx, config_gen_ectx, packet_front
		);
	}
}

// Demultiplex the handler output across the entry's pipelines.
static inline void
device_entry_ectx_dispatch(
	struct dp_worker *dp_worker,
	struct device_entry_ectx *entry_ectx,
	struct config_gen_ectx *config_gen_ectx,
	struct packet_front *packet_front
) {
	if (entry_ectx->pipeline_map_size == 0) {
		device_entry_ectx_drain(
			dp_worker, entry_ectx, config_gen_ectx, packet_front
		);
	} else if (entry_ectx->pipeline_count == 1) {
		device_entry_ectx_dispatch_single(
			dp_worker, entry_ectx, config_gen_ectx, packet_front
		);
	} else {
		device_entry_ectx_dispatch_many(
			dp_worker, entry_ectx, config_gen_ectx, packet_front
		);
	}
}

static inline void
device_ectx_process_entry(
	struct dp_worker *dp_worker,
	struct config_gen_ectx *config_gen_ectx,
	struct device_ectx *device_ectx,
	struct device_entry_ectx *entry_ectx,
	struct packet_front *packet_front
) {
	entry_ectx->handler(dp_worker, device_ectx, packet_front);

	counter_add_packets_bytes(
		entry_ectx->counter_packet_entry,
		packet_front_output_count(packet_front),
		packet_front_output_bytes(packet_front)
	);
	// Handler drops are all the front's drop list holds at this point:
	// chain drops are spliced straight to the round's sink and credited
	// through the chain's sinks, and drain drops credit the entry
	// counter where they happen.
	counter_add_packets_bytes(
		entry_ectx->counter_packet_drop,
		packet_front_drop_count(packet_front),
		packet_front_drop_bytes(packet_front)
	);

	device_entry_ectx_dispatch(
		dp_worker, entry_ectx, config_gen_ectx, packet_front
	);
}

void
device_ectx_process_input(
	struct dp_worker *dp_worker,
	struct config_gen_ectx *config_gen_ectx,
	struct device_ectx *device_ectx,
	struct packet_front *packet_front
) {
	struct device_entry_ectx *entry_ectx = device_ectx->abs_input_pipelines;

	device_ectx_process_entry(
		dp_worker,
		config_gen_ectx,
		device_ectx,
		entry_ectx,
		packet_front
	);
}

void
device_ectx_process_output(
	struct dp_worker *dp_worker,
	struct config_gen_ectx *config_gen_ectx,
	struct device_ectx *device_ectx,
	struct packet_front *packet_front
) {
	struct device_entry_ectx *entry_ectx =
		device_ectx->abs_output_pipelines;

	device_ectx_process_entry(
		dp_worker,
		config_gen_ectx,
		device_ectx,
		entry_ectx,
		packet_front
	);
}
