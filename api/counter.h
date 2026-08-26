#pragma once

#include <stddef.h>
#include <stdint.h>

#include "common/strutils.h"
#include "lib/counters/counters.h"
#include "lib/errors/errors.h"

struct dp_config;

#define COUNTER_TAG_KEY_LEN 80
#define COUNTER_TAG_VALUE_LEN 80

// A fixed-size (key, value) pair: tags travel by value inside predicates
// and handle snapshots, so no string outlives the struct that carries it.
struct counter_tag {
	char key[COUNTER_TAG_KEY_LEN];
	char value[COUNTER_TAG_VALUE_LEN];
};

// Return a tag with key and value truncated to the fixed field sizes.
static inline struct counter_tag
counter_tag_init(const char *key, const char *value) {
	struct counter_tag tag;
	(void)strtcpy(tag.key, key, sizeof(tag.key));
	(void)strtcpy(tag.value, value, sizeof(tag.value));
	return tag;
}

struct counter_handle {
	char name[COUNTER_NAME_LEN];
	uint64_t size;
	uint64_t gen;
	struct counter_tag *tags;
	size_t tag_count;

	// Per-instance snapshot arrays: values[i] holds size values for
	// instance i, copied when the list was acquired, remaining valid and
	// unchanged across controlplane updates until the list is freed.
	uint64_t **values;
};

// A list of counter handles whose single allocation also owns every
// handle's tag array and value blocks: they are carved out of a value
// region placed right after the handle array, so freeing the list
// reclaims all of it.
struct counter_handle_list {
	uint64_t instance_count;
	uint64_t count;
	struct counter_handle counters[];
};

// A counter-name filter, applied to every counter a read walks.
struct counter_query;

// Why a compile failed, a bad request apart from this side's own failure.
enum yanet_counter_query_result {
	YANET_COUNTER_QUERY_OK,
	YANET_COUNTER_QUERY_REJECTED,
	YANET_COUNTER_QUERY_NOMEM,
};

// Compile whole-name patterns in Rust regex syntax, any of which may match.
enum yanet_counter_query_result
yanet_counter_query_compile(
	const char *const *patterns,
	size_t count,
	struct counter_query **out,
	yanet_error **err
);

// Release a compiled query. No-op on NULL.
void
yanet_counter_query_free(struct counter_query *query);

// The name-parameter reads below select storages by their entity-name
// tags; entity names are truncated to the tag field sizes before the
// comparison, matching any entity whose name shares the first
// COUNTER_TAG_VALUE_LEN - 1 characters. Every name a configuration
// accepts fits untruncated, so this only relaxes the match for
// over-long caller-supplied names.
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

// Get module counters, optionally filtered by the compiled name query.
// Returns the module's predefined counters only; its per-rule counters
// live on runtime-kind storages read through yanet_get_counters_by_tags.
struct counter_handle_list *
yanet_get_module_counters(
	struct dp_config *dp_config,
	const char *device_name,
	const char *pipeline_name,
	const char *function_name,
	const char *chain_name,
	const char *module_type,
	const char *module_name,
	const struct counter_query *query
);

struct counter_handle_list *
yanet_get_object_counters(
	struct dp_config *dp_config,
	const char *object_type,
	const char *object_name
);

struct counter_handle_list *
yanet_get_worker_counters(struct dp_config *dp_config);

// Counters of a single DPDK port.
//
// port_id/port_name identify the port; counters holds that port's xstats as a
// standard counter_handle_list.
struct port_counter_group {
	uint16_t port_id;
	char port_name[80];
	struct counter_handle_list *counters;
};

// Per-port counter groups for every DPDK port of an instance.
struct port_counter_group_list {
	uint64_t port_count;
	struct port_counter_group ports[];
};

// Collect counters for every DPDK port, grouped per port.
//
// The returned list must be released with yanet_port_counter_group_list_free.
// Returns NULL on allocation failure or if the per-port counter array
// offset is not yet published for a nonzero port count.
struct port_counter_group_list *
yanet_get_port_counters(struct dp_config *dp_config);

struct port_counter_group *
yanet_get_port_counter_group(
	struct port_counter_group_list *groups, uint64_t idx
);

void
yanet_port_counter_group_list_free(struct port_counter_group_list *groups);

struct worker_counter_metadata {
	uint32_t core_id;
	uint32_t device_id;
	uint32_t queue_id;
	uint32_t rx_burst_size;
};

int
yanet_get_worker_counter_metadata(
	struct dp_config *dp_config,
	uint64_t worker_idx,
	struct worker_counter_metadata *metadata
);

struct counter_handle *
yanet_get_counter(struct counter_handle_list *counters, uint64_t idx);

uint64_t
yanet_get_counter_value(
	uint64_t **values, uint64_t value_idx, uint64_t worker_idx
);

// Copy all values for every instance of a counter into a flat caller-supplied
// buffer in a single call.
//
// values_out must have room for instance_count * size uint64 elements and is
// filled instance-major: instance i occupies values_out[i*size .. i*size +
// size). Performs the same reads as yanet_get_counter_value but batched into
// one call, avoiding per-value CGO overhead when reading counters from Go.
void
yanet_get_counter_values(
	uint64_t **values,
	uint64_t size,
	uint64_t instance_count,
	uint64_t *values_out
);

// Return counters that satisfy every predicate in tags and the compiled
// name query. Pass tag_count == 0 to impose no per-tag constraint and a
// NULL query to impose no name constraint.
//
// Each worker's counter storage registry is matched independently and
// the result is the union across workers: every returned counter spans
// instance_count == worker_count instances, and a worker whose registry
// does not carry the counter contributes zero values for its instance.
//
// Each counter_tag is a predicate against the counter's tags, with
// the check encoded in value: an empty string requires the tag to be
// absent, "*" requires the tag to be present with any value, and any
// other string requires the tag to be present with exactly that
// value.
//
// Recognized keys are "device", "pipeline", "function", "chain",
// "module_type", "module_name", and "kind". The "kind" value names the
// owner level: "device", "pipeline", "function", "chain", "module",
// "runtime", or "object". A "runtime" storage is module-owned and
// additionally carries a "config" tag naming its counter registry.
//
// A tag is rejected with err filled and NULL returned if any of the
// following holds: key is unrecognized; or tags contains another
// predicate with the same key. Keys and values are copied into the
// tags' fixed-size fields, truncating anything longer than
// COUNTER_TAG_KEY_LEN - 1 / COUNTER_TAG_VALUE_LEN - 1 characters; the
// Go bindings reject such tags before the call instead.
//
// The returned list must be released with yanet_counter_handle_list_free.
// On failure NULL is returned and err is filled; an empty match is a
// non-NULL empty list.
struct counter_handle_list *
yanet_get_counters_by_tags(
	struct dp_config *dp_config,
	const struct counter_tag *tags,
	size_t tag_count,
	const struct counter_query *query,
	yanet_error **err
);

// One worker's independently matched counter set.
//
// counters holds only that worker's snapshot: its instance_count is 1
// and every handle's values are copied from that worker's own storages.
struct counter_worker_set {
	uint64_t worker_idx;
	struct counter_handle_list *counters;
};

// Per-worker counter sets, one entry per dataplane worker in index
// order.
//
// A worker whose registry holds no match carries an empty set, so entry
// i always belongs to worker i.
struct counter_worker_set_list {
	uint64_t worker_count;
	struct counter_worker_set sets[];
};

// Return each worker's counters that satisfy every predicate in tags
// and the compiled name query, matched against that worker's own
// counter storage registry.
//
// The tag predicates and their value semantics are those of
// yanet_get_counters_by_tags; unlike it, no cross-worker union is
// taken, so the sets may differ from worker to worker.
//
// The returned list must be released with
// yanet_counter_worker_set_list_free. On failure NULL is returned and
// err is filled; a worker without a match yields an empty set, not
// failure.
struct counter_worker_set_list *
yanet_get_counters_by_tags_per_worker(
	struct dp_config *dp_config,
	const struct counter_tag *tags,
	size_t tag_count,
	const struct counter_query *query,
	yanet_error **err
);

// Return the set belonging to worker_idx, or NULL when out of range.
struct counter_worker_set *
yanet_get_counter_worker_set(
	struct counter_worker_set_list *sets, uint64_t worker_idx
);

// Release a per-worker set list. No-op on NULL.
void
yanet_counter_worker_set_list_free(struct counter_worker_set_list *sets);

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
	/** Total (summary) processing latency in nanoseconds across all batches
	 */
	uint64_t summary_latency;

	/** Total number of packets transmitted for this batch size range */
	uint64_t packets;

	/** Total number of bytes processed for this batch size range */
	uint64_t bytes;

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
 * summary latency and a detailed histogram of latency measurements.
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
 * @param err Error pointer for error reporting. On failure, this will
 *            contain a detailed error message. The caller must call
 *            yanet_error_free() to free the error when done.
 * @return 0 on success, -1 on failure (check err for error details)
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
	yanet_error **err
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
