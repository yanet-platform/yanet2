#include "config.h"

#include <stdlib.h>

#include "common/strutils.h"
#include "lib/dataplane/object/object.h"

// Inert dataplane-side descriptor for the l3b_virtual_service object type.
//
// Objects carry no handler and take no per-packet action: the constructor
// only registers the type name so the controlplane can resolve
// cp_object_init against the dataplane's object-type registry. The typed
// object itself lives in modules/l3b/api.
struct object *
new_object_l3b_virtual_service() {
	struct object *object = (struct object *)malloc(sizeof(struct object));
	if (object == NULL) {
		return NULL;
	}
	strtcpy(object->name,
		L3B_VIRTUAL_SERVICE_OBJECT_TYPE,
		sizeof(object->name));
	return object;
}
