#pragma once

#include <stddef.h>
#include <stdint.h>

#include "lib/controlplane/diag/diag.h"

struct dp_config;

struct counter_value_handle;

struct counter_handle {
	char name[60];
	uint64_t size;
	uint64_t gen;
	struct counter_value_handle *value_handle;
};

struct counter_handle_list {
	uint64_t instance_count;
	uint64_t count;
	struct counter_handle counters[];
};

struct counter_handle_list *
yanet_get_device_counters(struct dp_config *dp_config, const char *device_name);

struct counter_handle_list *
yanet_get_pipeline_counters(
	struct dp_config *dp_config,
	const char *device_name,
	const char *pipeline_name
);

struct counter_handle_list *
yanet_get_function_counters(
	struct dp_config *dp_config,
	const char *device_name,
	const char *pipeline_name,
	const char *function_name
);

struct counter_handle_list *
yanet_get_chain_counters(
	struct dp_config *dp_config,
	const char *device_name,
	const char *pipeline_name,
	const char *function_name,
	const char *chain_name
);

// Get module counters, optionally filtered by name.
struct counter_handle_list *
yanet_get_module_counters(
	struct dp_config *dp_config,
	const char *device_name,
	const char *pipeline_name,
	const char *function_name,
	const char *chain_name,
	const char *module_type,
	const char *module_name,
	const char *const *query,
	size_t query_count
);

struct counter_handle_list *
yanet_get_worker_counters(struct dp_config *dp_config);

struct counter_handle *
yanet_get_counter(struct counter_handle_list *counters, uint64_t idx);

uint64_t
yanet_get_counter_value(
	struct counter_value_handle *value_handle,
	uint64_t value_idx,
	uint64_t worker_idx
);

void
yanet_counter_handle_list_free(struct counter_handle_list *counters);

/**
 * Represents a single latency bucket in a performance histogram.
 *
 * Each bucket tracks how many packet batches were processed with latency
 * greater than or equal to min_latency. The histogram uses a hybrid approach
 * with linear buckets for fine-grained resolution at typical latencies and
 * exponential buckets for efficient coverage of outliers.
 */
struct module_performance_counter_latency_range {
	/** Minimum latency in nanoseconds for this bucket */
	uint64_t min_latency;

	/** Number of packet batches that fell into this latency bucket */
	size_t batches;
};

/**
 * Performance metrics for a specific packet batch size range.
 *
 * Modules process packets in batches, and this structure contains latency
 * statistics for a particular batch size range (e.g., 1 packet, 2-3 packets,
 * 4-7 packets, etc.). The latency distribution is captured using a hybrid
 * histogram with both linear and exponential buckets.
 */
struct module_performance_counter {
	/** Mean processing latency in nanoseconds across all batches */
	float mean_latency;

	/** Minimum batch size for this counter (e.g., 1, 2, 4, 8, 16, 32) */
	uint64_t min_batch_size;

	/** Number of latency histogram buckets */
	size_t latency_ranges_count;

	/** Array of latency histogram buckets, sorted by increasing min_latency
	 */
	struct module_performance_counter_latency_range *latency_ranges;
};

/**
 * Collection of all performance counters for a module.
 *
 * Contains performance metrics for all 6 batch size ranges tracked by the
 * module: 1, 2-3, 4-7, 8-15, 16-31, and 32+ packets. Each counter includes
 * mean latency and a detailed histogram of latency measurements.
 */
struct module_performance_counters {
	/** Number of performance counters (typically 6, one per batch size
	 * range) */
	size_t counters_count;

	/** Array of performance counters, ordered by min_batch_size */
	struct module_performance_counter *counters;

	/** Total number of packets transmitted by the module across all workers
	 */
	uint64_t tx;

	/** Total number of packets received by the module across all workers */
	uint64_t rx;

	/** Total number of bytes transmitted by the module across all workers
	 */
	uint64_t tx_bytes;

	/** Total number of bytes received by the module across all workers */
	uint64_t rx_bytes;
};

/**
 * Retrieve module performance counters from the dataplane configuration.
 *
 * This function extracts and aggregates performance metrics for a specific
 * module across all worker threads. The metrics include:
 * - Latency histograms for different packet batch sizes (1, 2-3, 4-7, 8-15,
 *   16-31, 32+ packets)
 * - TX/RX packet counters aggregated across all workers
 * - TX/RX byte counters aggregated across all workers
 *
 * The returned structure must be freed using
 * yanet_module_performance_counters_free() when no longer needed.
 *
 * @param counters Output parameter for the performance counters structure.
 *                 On success, this will be populated with performance data
 *                 including latency histograms and tx/rx statistics.
 * @param dp_config Pointer to the dataplane configuration
 * @param device_name Name of the device
 * @param pipeline_name Name of the pipeline
 * @param function_name Name of the function
 * @param chain_name Name of the chain
 * @param module_type Type identifier of the module
 * @param module_name Name identifier of the module
 * @param diag Diagnostic structure for error reporting. On failure, this will
 *             contain a detailed error message that can be retrieved using
 *             diag_msg() or diag_take_msg(). The caller must call diag_reset()
 *             to free the error message when done.
 * @return 0 on success, -1 on failure (check diag for error details)
 */
int
yanet_module_performance_counters(
	struct module_performance_counters *counters,
	struct dp_config *dp_config,
	const char *device_name,
	const char *pipeline_name,
	const char *function_name,
	const char *chain_name,
	const char *module_type,
	const char *module_name,
	struct diag *diag
);

/**
 * Free resources allocated for module performance counters.
 *
 * Releases all memory allocated by yanet_module_performance_counters(),
 * including the latency_ranges arrays within each counter.
 *
 * @param counters Pointer to the performance counters structure to free
 */
void
yanet_module_performance_counters_free(
	struct module_performance_counters *counters
);
