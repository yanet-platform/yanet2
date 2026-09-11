#pragma once

#define MODULE_TYPE_LEN 80

// Dataplane <-> module .so ABI version.
//
// Bump this monotonically whenever the layout of a struct or function
// signature shared between the dataplane binary and a module .so changes.
//
// The dataplane's plugin loader rejects a .so whose exported version does
// not match this constant.
#define YANET_MODULE_ABI_VERSION 29

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

// Commit handler: a preparation hook, run exactly once per published
// generation per shared module config.
//
// Runs inside the dataplane address space, from exactly one thread,
// before the generation's contexts reach the workers. Only
// generation-invariant derivation — from the item's own content
// alone, like absolutizing its own internal pointers — may be
// written; the item is shared with older generations whose workers
// read it concurrently and observe the write. Controlplane-owned
// relative fields stay authoritative, and anything per-worker or
// referencing generation-owned memory belongs in the ectx layer
// instead. The thread holds no control-plane lock in production and
// is the installer holding the control-plane configuration lock in
// the harness; taking that lock self-deadlocks.
typedef void (*module_commit_handler)(
	struct dp_config *dp_config, struct cp_module *cp_module
);

struct module {
	char name[MODULE_TYPE_LEN];
	module_handler handler;
	module_commit_handler commit_handler;
};

typedef struct module *(*module_load_handler)();
