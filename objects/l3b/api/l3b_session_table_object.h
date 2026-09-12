#pragma once

#include <stdbool.h>
#include <stdint.h>

#include "lib/controlplane/config/cp_object.h"
#include "lib/errors/errors.h"
#include "lib/statemap/fwtable.h"

struct agent;

// Shared-memory object type under which per-service session tables are
// registered. A session table carries the same name as its virtual service;
// the distinct type keeps the two registry entries apart.
#define L3B_SESSION_TABLE_OBJECT_TYPE "l3b_session_table"

/*
 * A per-service session table: a layered statemap table pinning client flows
 * to real servers (see lib/l3state).
 *
 * The cp_object header carries the (type, name) identity and the generation
 * accounting; the owning virtual service object holds the relative pointer
 * through which the dataplane reaches the table.
 */
struct l3b_session_table_object {
	struct cp_object cp_object;
	fwtable_t table;
};

// Allocate a named session table object in the agent's shared memory and
// install its first layer. index_size and extra_bucket_count size the layer's
// hash index and bucket store (zero selects the defaults); worker_count must
// cover every worker that will pin sessions. The object is registered
// under (L3B_SESSION_TABLE_OBJECT_TYPE, name) and is published through
// agent_update_objects.
struct cp_object *
l3b_session_table_object_create(
	struct agent *agent,
	const char *name,
	uint16_t worker_count,
	uint32_t index_size,
	uint32_t extra_bucket_count,
	yanet_error **err
);

// Destroy the session table object when it is dangling — referenced by no
// live configuration generation. A refused destroy is reported through err
// and the caller must retry later.
int
l3b_session_table_object_free(struct cp_object *cp_object, yanet_error **err);

// Release the table's layers and the object struct itself. Internal to the
// l3b objects: run only after cp_object_try_destroy granted the exclusive
// right (l3b_virtual_service_free drives it for service-owned tables).
void
l3b_session_table_object_destroy(struct cp_object *cp_object);
