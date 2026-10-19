// A plugin shipping an object type with a commit hook and a prepared size
// of its own, so a test can tell that the loader resolved its constructor.

#include <stdlib.h>

#include "common/strutils.h"
#include "lib/dataplane/object/object.h"

#include "loader_probe.h"

__attribute__((visibility("default"))) void
loader_probe_plugin_hook(
	struct object_ectx *object_ectx, struct cp_object *cp_object
) {
	(void)object_ectx;
	(void)cp_object;
}

__attribute__((visibility("default"))) struct object *
new_object_loader_probe(void) {
	struct object *object =
		(struct object *)calloc(1, sizeof(struct object));
	if (object == NULL) {
		return NULL;
	}
	strtcpy(object->name, LOADER_PROBE_NAME, sizeof(object->name));
	object->commit_ectx_handler = loader_probe_plugin_hook;
	object->prepared_size = LOADER_PROBE_PLUGIN_PREPARED_SIZE;
	return object;
}
