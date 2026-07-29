#pragma once

/*
 * Dataplane-side descriptor for an inert object type.
 *
 * Objects are inert: unlike modules and devices they carry no handler
 * and take no per-packet action. A loaded object type exists only so the
 * controlplane can resolve a cp_object's type name to a stable dataplane
 * index (dp_object_idx) at init time, mirroring dp_module_idx /
 * dp_device_idx.
 *
 * The load-handler typedef is resolved by dp_load_object from a
 * new_object_<name> constructor exported by the main binary, parallel to
 * dp_load_module and dp_load_device.
 */
#define OBJECT_TYPE_LEN 80

struct object {
	char name[OBJECT_TYPE_LEN];
};

typedef struct object *(*object_load_handler)();
