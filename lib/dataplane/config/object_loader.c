#include "object_loader.h"

#include <dlfcn.h>
#include <stdio.h>
#include <stdlib.h>

#include "common/exp_array.h"
#include "common/memory_address.h"
#include "common/strutils.h"
#include "lib/dataplane/config/zone.h"
#include "lib/dataplane/object/object.h"
#include "lib/logging/log.h"

int
dp_load_object(struct dp_config *dp_config, void *bin_hndl, const char *name) {
	LOG(INFO, "load object %s", name);
	char loader_name[128];
	snprintf(loader_name, sizeof(loader_name), "%s%s", "new_object_", name);
	object_load_handler loader =
		(object_load_handler)dlsym(bin_hndl, loader_name);
	if (loader == NULL) {
		LOG(ERROR, "failed to load dyn symbol %s", loader_name);
		return -1;
	}
	struct object *object = loader();
	if (object == NULL) {
		LOG(ERROR, "failed to construct object %s", name);
		return -1;
	}

	struct dp_object *dp_objects = ADDR_OF(&dp_config->dp_objects);
	if (mem_array_expand_exp(
		    &dp_config->memory_context,
		    (void **)&dp_objects,
		    sizeof(*dp_objects),
		    &dp_config->object_count
	    )) {
		LOG(ERROR, "failed to allocate memory for object %s", name);
		// FIXME: free object
		return -1;
	}

	struct dp_object *dp_object = dp_objects + dp_config->object_count - 1;

	strtcpy(dp_object->name, object->name, sizeof(dp_object->name));

	SET_OFFSET_OF(&dp_config->dp_objects, dp_objects);

	free(object);

	return 0;
}
