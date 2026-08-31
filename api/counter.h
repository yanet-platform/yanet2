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

	// The counter's value snapshot: size values copied when the list
	// was acquired, remaining valid and unchanged across controlplane
	// updates until the list is freed.
	uint64_t *values;
};

// A list of counter handles whose single allocation also owns every
// handle's value block: it is carved out of a region placed right
// after the handle array, so freeing the list reclaims all of it.
struct counter_handle_list {
	uint64_t count;
	struct counter_handle counters[];
};

// The matched counters of one counter storage, folded into one block.
//
// Every counter of a storage carries the same tags, so the block
// states the storage's tag names and values once instead of repeating
// them per counter; its counters sit in registry order.
struct counter_group {
	uint64_t count;
	struct counter_tag *tags;
	size_t tag_count;
	struct counter_handle *counters;
};

// One worker's matched storages, one group per storage in match order.
//
// The list's single allocation also owns every group's handle array,
// tag-array copy and value block: they are carved out of a region
// placed right after the group array, so freeing the list reclaims
// all of it.
struct counter_group_list {
	uint64_t group_count;
	struct counter_group groups[];
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

// Snapshot the worker counters of one worker, identified by its index
// among the dataplane's workers.
//
// The returned list must be released with
// yanet_counter_handle_list_free. Returns NULL when the index is out of
// range or the allocation fails; the union across workers is the
// caller's to build.
struct counter_handle_list *
yanet_get_worker_counters(struct dp_config *dp_config, uint64_t worker_idx);

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
yanet_get_counter_value(const uint64_t *values, uint64_t value_idx);

// Copy all values of a counter into a flat caller-supplied buffer in a
// single call.
//
// values_out must have room for size uint64 elements. Performs the same
// reads as the single-value accessor but batched into one call, avoiding
// per-value CGO overhead when reading counters from Go.
void
yanet_get_counter_values(
	const uint64_t *values, uint64_t size, uint64_t *values_out
);

// One worker's independently matched counter set.
//
// The set holds only that worker's snapshot: one group per matched
// storage, every counter value copied from that worker's own storages.
// The set list owns every group list; releasing it releases them all.
struct counter_worker_set {
	uint64_t worker_idx;
	struct counter_group_list *groups;
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
// and the compiled name query. Pass tag_count == 0 to impose no
// per-tag constraint and a NULL query to impose no name constraint.
//
// Each worker's counter storage registry is matched independently, so
// the sets may differ from worker to worker; the union across workers
// is the caller's to build.
//
// The counters of one storage are folded into a single counter_group
// carrying that storage's tag names and values once, not once per
// counter; a storage whose counters all fail the name query yields no
// group at all.
//
// Each counter_tag is a predicate against the counter's tags, with the
// check encoded in value: an empty string requires the tag to be
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
// That key list names what the built-in storages carry, not an allowed
// vocabulary: any other key is accepted, matching only the storages
// that carry it.
//
// A tag is rejected with err filled and NULL returned if any of the
// following holds: the tag count exceeds its limit; a key or value is
// longer than COUNTER_TAG_KEY_LEN - 1 / COUNTER_TAG_VALUE_LEN - 1
// characters, since nothing is truncated on the way in; or tags
// contains another predicate with the same key.
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

// Return the group at idx of a worker's group list, or NULL when out
// of range.
struct counter_group *
yanet_get_counter_group(struct counter_group_list *groups, uint64_t idx);

// Return the counter at idx of a group, or NULL when out of range.
struct counter_handle *
yanet_get_group_counter(struct counter_group *group, uint64_t idx);

// Release a per-worker set list, and with it every group list it
// holds. No-op on NULL.
void
yanet_counter_worker_set_list_free(struct counter_worker_set_list *sets);

void
yanet_counter_handle_list_free(struct counter_handle_list *counters);
