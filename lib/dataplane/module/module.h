#pragma once

#define MODULE_TYPE_LEN 80

// Dataplane <-> module .so ABI version.
//
// Bump this monotonically whenever the layout of a struct or function
// signature shared between the dataplane binary and a module .so changes.
//
// The dataplane's plugin loader rejects a .so whose exported version does
// not match this constant.
#define YANET_MODULE_ABI_VERSION 1

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

struct module {
	char name[MODULE_TYPE_LEN];
	module_handler handler;
};

typedef struct module *(*module_load_handler)();
