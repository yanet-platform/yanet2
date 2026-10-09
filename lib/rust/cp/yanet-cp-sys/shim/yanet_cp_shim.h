#pragma once

// C entry points the Rust control plane needs from the agent library.
//
// Only primitive types and opaque pointers cross this header, so the Rust
// side declares it by hand and needs no bindgen. Errors come back as a
// message in a caller buffer.

#include <stdint.h>

struct agent;
struct cp_device;

// Sizes and field offsets of the C structures Rust walks.
struct yanet_cp_shim_layout {
	uint64_t cp_device_size;
	uint64_t cp_device_align;
	// Offsets of the relative slots of the common device header.
	uint64_t cp_device_agent;
	uint64_t cp_device_input;
	uint64_t cp_device_output;
	// Offsets of the arena table of an agent and of its entries.
	uint64_t agent_arena_count;
	uint64_t agent_arenas;
	uint64_t arena_size;
	uint64_t arena_data;
	uint64_t arena_len;
	// Capacity of the name fields, including the terminating zero.
	uint64_t device_name_len;
	uint64_t pipeline_name_len;
};

void
yanet_cp_shim_layout(struct yanet_cp_shim_layout *layout);

struct yanet_cp_shim_pipeline {
	const char *name;
	uint64_t weight;
};

struct yanet_cp_shim_device_request {
	const char *type;
	const char *name;
	// Size of the whole device block, the common header included.
	uint64_t size;
	// Configuration layout the device type must have been loaded with.
	uint64_t config_layout;
	const struct yanet_cp_shim_pipeline *input;
	uint64_t input_count;
	const struct yanet_cp_shim_pipeline *output;
	uint64_t output_count;
};

// Allocate a zeroed device block from the agent and initialize its common
// header.
//
// The device is dangling until an update publishes it. Returns NULL with a
// message in err on failure; precondition is set to 1 when the dataplane
// lacks the device type or has it loaded for another layout, 0 otherwise.
struct cp_device *
yanet_cp_shim_device_new(
	struct agent *agent,
	const struct yanet_cp_shim_device_request *request,
	int32_t *precondition,
	char *err,
	uint64_t err_len
);

// Destroy a dangling device block of the given size.
//
// Returns 0 when destroyed, 1 while a live generation still references the
// device (it stays intact), -1 with a message in err on failure.
int
yanet_cp_shim_device_free(
	struct cp_device *device, uint64_t size, char *err, uint64_t err_len
);

// Copy len bytes at offset of the live device with the given type and name
// in the active generation, under the configuration lock.
//
// Returns 0 when copied, 1 when no such device exists, 2 when the device
// type was loaded for another configuration layout than the one given.
int
yanet_cp_shim_device_read(
	struct agent *agent,
	const char *type,
	const char *name,
	uint64_t config_layout,
	uint64_t offset,
	void *out,
	uint64_t len
);
