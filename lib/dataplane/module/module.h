#pragma once

#include <stdint.h>

#define MODULE_TYPE_LEN 80

// Dataplane <-> module .so ABI version.
//
// Bump this monotonically whenever the layout of a struct or function
// signature shared between the dataplane binary and a module .so changes.
//
// The dataplane's plugin loader rejects a .so whose exported version does
// not match this constant.
//
// 32: struct packet dropped the module_device_id field (56 -> 48 bytes).
// 33: struct module_ectx replaced the device index tables with the
// per-device routing targets and the module device id.
// 34: struct object_ectx gained the object's private per-context
// prepared buffer fields and the owning worker index.
// 35: struct device_entry_ectx took the padding after the direction
// field for the device index routing stamps onto packets.
// 36: struct config_gen_ectx gained the egress backing table with its
// absolute copy and the worker's receive device slot; struct
// device_ectx gained its registry device index.
// 37: struct config_gen_ectx gained the per-physical-device transmit
// buckets the output-entry terminals fill and the write stage drains.
// 38: struct packet dropped the unused receive device field.
// 39: struct dp_worker replaced its single device_id/queue_id pair with
// an assignment list (device, queue per polled queue), and struct
// config_gen_ectx replaced the worker's receive device slot with the
// reverse backing table and its absolute copy.
// 40: struct packet dropped its device field — the executing device is
// derived from the pipeline context, and struct module_ectx gained the
// registry device of its placement.
// 41: struct dp_config gained the last committed generation pointer.
// 42: struct module and struct dp_module gained config_layout.
#define YANET_MODULE_ABI_VERSION 42

// Symbol name a module .so exports carrying its compiled-against
// YANET_MODULE_ABI_VERSION, as a uint32_t global.
#define YANET_MODULE_ABI_VERSION_SYMBOL "yanet_module_abi_version"

// Marks the module constructor symbol for export from the module object.
//
// Module dataplane objects are compiled with hidden visibility, so the
// new_module_<name> constructor would otherwise be unreachable by the
// plugin loader. Default visibility exports exactly that one symbol.
#define YANET_MODULE_EXPORT __attribute__((visibility("default")))

struct packet_front;
struct module_ectx;
struct dp_worker;
struct dp_config;
struct cp_module;

/*
 * Module handler called for a pipeline front.
 * Module should go through the front and handle packets.
 * For each input packet module should put into output or drop list of the
 * front.
 * Also module may create new packet and put the into output queue.
 */
typedef void (*module_handler)(
	struct dp_worker *dp_worker,
	struct module_ectx *module_ectx,
	struct packet_front *packet_front
);

// Commit handler: a preparation hook, run exactly once per config
// item, before any worker can see it.
//
// Runs inside the dataplane address space, from exactly one thread,
// when the item first appears in a published generation; a later
// generation carrying the same item does not run it again. Only
// generation-invariant derivation — from the item's own content
// alone, like absolutizing its own internal pointers — may be
// written. Controlplane-owned relative fields stay authoritative,
// and anything per-worker or referencing generation-owned memory
// belongs in the ectx layer instead. The thread holds no
// control-plane lock in production and is the installer holding the
// control-plane configuration lock in the harness; taking that lock
// self-deadlocks.
typedef void (*module_commit_handler)(
	struct dp_config *dp_config, struct cp_module *cp_module
);

// Execution-context commit handler: a per-worker derivation hook,
// run once per module
// execution context per published generation.
//
// Runs inside the dataplane address space during the per-worker
// absolutization pass, which re-derives everything from the
// authoritative fields, so a repeated pass rewrites the same values.
// Writes only the module's own private per-context buffer reached
// through the execution context; the shared module config and the
// context's authoritative fields are read-only here. Values that vary
// per worker belong in this hook, generation-invariant ones in the
// commit handler.
typedef void (*module_commit_ectx_handler)(
	struct module_ectx *module_ectx, struct cp_module *cp_module
);

struct module {
	char name[MODULE_TYPE_LEN];
	module_handler handler;
	module_commit_handler commit_handler;
	module_commit_ectx_handler commit_ectx_handler;
	// Size of the module's private per-context buffer, zero when the
	// module needs none. The control plane allocates a zeroed buffer
	// of this size for every module execution context it builds.
	uint64_t prepared_size;
	// Layout identity of the module's configuration, zero when unchecked.
	//
	// A module whose handler reads its configuration through a typed
	// layout declares a non-zero value here; the control plane then
	// creates configurations for it only through an init that names the
	// same value, so the handler never sees bytes of another layout.
	uint64_t config_layout;
};

typedef struct module *(*module_load_handler)();
