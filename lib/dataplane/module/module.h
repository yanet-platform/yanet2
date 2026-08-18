#pragma once

#define MODULE_TYPE_LEN 80

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
