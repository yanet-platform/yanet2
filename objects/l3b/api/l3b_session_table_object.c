#include "l3b_session_table_object.h"

#include <stdlib.h>
#include <string.h>

#include "common/container_of.h"
#include "common/strutils.h"
#include "lib/controlplane/agent/agent.h"
#include "lib/dataplane/object/object.h"

struct cp_object *
l3b_session_table_object_create(
	struct agent *agent,
	const char *name,
	uint16_t worker_count,
	uint32_t index_size,
	uint32_t extra_bucket_count,
	yanet_error **err
) {
	struct l3b_session_table_object *object =
		(struct l3b_session_table_object *)memory_balloc(
			&agent->memory_context,
			sizeof(struct l3b_session_table_object)
		);
	if (object == NULL) {
		yanet_error_add(err, "failed to allocate session table object");
		return NULL;
	}
	memset(object, 0, sizeof(struct l3b_session_table_object));

	if (cp_object_init(
		    &object->cp_object,
		    agent,
		    L3B_SESSION_TABLE_OBJECT_TYPE,
		    name,
		    err
	    )) {
		yanet_error_add(err, "failed to init session table object");
		memory_bfree(
			&agent->memory_context,
			object,
			sizeof(struct l3b_session_table_object)
		);
		return NULL;
	}

	// Layer memory is allocated through the object's own memory context,
	// so the session stores are attributed to the table object.
	fwmap_config_t config = {0};
	config.key_size = sizeof(struct l3b_session_key);
	config.value_size = sizeof(struct l3b_session_value);
	config.hash_fn_id = FWMAP_HASH_FNV1A;
	config.rand_fn_id = FWMAP_RAND_DEFAULT;
	config.copy_key_fn_id = FWMAP_COPY_KEY_DEFAULT;
	// A re-pin overwrites the record; a promoted record keeps the real the
	// session was already pinned to.
	config.update_value_fn_id = FWMAP_UPDATE_VALUE_DEFAULT;
	config.promote_value_fn_id = FWMAP_PROMOTE_VALUE_KEEP_OLD;
	config.worker_count = worker_count;
	config.index_size =
		index_size != 0 ? index_size : L3B_SESSION_INDEX_SIZE;
	config.extra_bucket_count = extra_bucket_count != 0
					    ? extra_bucket_count
					    : L3B_SESSION_EXTRA_BUCKETS;

	if (fwtable_insert_layer_cp(
		    &object->table, &config, &object->cp_object.memory_context
	    )) {
		yanet_error_add(err, "failed to insert session table layer");
		cp_object_fini(&object->cp_object);
		memory_bfree(
			&agent->memory_context,
			object,
			sizeof(struct l3b_session_table_object)
		);
		return NULL;
	}

	return &object->cp_object;
}

// Release every layer and the object struct itself; internal to the l3b
// object pair, run by the owner once cp_object_try_destroy granted the
// exclusive right (directly here, or through l3b_virtual_service_free).
void
l3b_session_table_object_destroy(struct cp_object *cp_object) {
	struct l3b_session_table_object *object = container_of(
		cp_object, struct l3b_session_table_object, cp_object
	);

	// Destruction runs with no concurrent readers: release the parked
	// layers first, then the whole active chain.
	fwtable_free_stale(&object->table, &object->cp_object.memory_context);
	fwmap_t *layer = ADDR_OF(&object->table.head);
	while (layer != NULL) {
		fwmap_t *next = (fwmap_t *)ADDR_OF(&layer->next);
		fwmap_free(layer, &object->cp_object.memory_context);
		layer = next;
	}

	// Capture agent before fini zeroes it.
	struct agent *agent = ADDR_OF(&cp_object->agent);

	cp_object_fini(cp_object);
	memory_bfree(
		&agent->memory_context,
		object,
		sizeof(struct l3b_session_table_object)
	);
}

int
l3b_session_table_object_free(struct cp_object *cp_object, yanet_error **err) {
	if (cp_object_try_destroy(cp_object, err)) {
		return -1;
	}

	l3b_session_table_object_destroy(cp_object);
	return 0;
}

struct object *
new_object_l3b_session_table() {
	struct object *object = (struct object *)malloc(sizeof(struct object));
	if (object == NULL) {
		return NULL;
	}
	strtcpy(object->name,
		L3B_SESSION_TABLE_OBJECT_TYPE,
		sizeof(object->name));
	return object;
}
