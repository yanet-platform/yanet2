#pragma once

#include <stdbool.h>
#include <stdint.h>

#include "lib/controlplane/config/cp_object.h"
#include "lib/errors/errors.h"
#include "lib/fwstate/fwmap.h"
#include "lib/fwstate/fwtable.h"

struct agent;

// Shared-memory object type under which per-service session tables are
// registered. A session table carries the same name as its virtual service;
// the distinct type keeps the two registry entries apart.
#define L3B_SESSION_TABLE_OBJECT_TYPE "l3b_session_table"

// Default lifetime of a session record, in seconds; an entry past its
// deadline is treated as absent and the flow picks a fresh real server.
#define L3B_SESSION_TTL_SECONDS 300

// Default hash index size of a session table layer, in slots.
#define L3B_SESSION_INDEX_SIZE (1024u * 1024u)

// Default extra bucket count of a session table layer.
#define L3B_SESSION_EXTRA_BUCKETS 1024u

/*
 * Session identity: the client's source address and source port.
 *
 * family discriminates the address family and selects how many bytes of
 * src_addr carry the address (4 for IPv4, zero-padded; 16 for IPv6), so one
 * table holds both families without cross-family key collisions. The
 * reserved byte keeps src_port aligned and must stay zero.
 */
struct l3b_session_key {
	uint8_t family;
	uint8_t reserved;
	uint16_t src_port; // little-endian
	uint8_t src_addr[16];
};

// The real server index a session is pinned to.
struct l3b_session_value {
	uint32_t real_index;
};

/*
 * A per-service session table: a layered fwtable mapping session keys to the
 * real server each flow was pinned to.
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
// cover every worker that will insert sessions. The object is registered
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

/*
 * Look up the real server a session is pinned to.
 *
 * Returns 0 and stores the real index on hit, or -1 when the table has no
 * live record for the key. Entries past their deadline count as absent.
 */
static inline int
l3b_session_table_lookup(
	const struct l3b_session_table_object *table,
	uint64_t now,
	const struct l3b_session_key *key,
	uint32_t *real_index
) {
	struct l3b_session_value *value = NULL;
	rwlock_t *lock = NULL;
	bool from_stale = false;

	int64_t result = fwtable_lookup(
		&((struct l3b_session_table_object *)table)->table,
		now,
		key,
		(void **)&value,
		&lock,
		&from_stale
	);
	if (result < 0 || value == NULL) {
		if (lock != NULL) {
			rwlock_read_unlock(lock);
		}
		return -1;
	}

	uint32_t candidate = value->real_index;
	if (lock != NULL) {
		rwlock_read_unlock(lock);
	}

	*real_index = candidate;
	return 0;
}

/*
 * Pin a session to a real server.
 *
 * Inserts the record into the active layer with the given time-to-live; an
 * existing record in a deeper layer is promoted first-write-wins, so a
 * session keeps the real it was pinned to across layer rotations. Returns 0
 * on success, -1 when the active layer is full.
 */
static inline int
l3b_session_table_insert(
	const struct l3b_session_table_object *table,
	uint16_t worker_idx,
	uint64_t now,
	uint64_t ttl,
	const struct l3b_session_key *key,
	uint32_t real_index
) {
	const struct l3b_session_value value = {.real_index = real_index};
	rwlock_t *lock = NULL;

	int64_t result = fwtable_insert(
		&((struct l3b_session_table_object *)table)->table,
		worker_idx,
		now,
		ttl,
		key,
		&value,
		&lock
	);

	if (lock != NULL) {
		rwlock_write_unlock(lock);
	}
	return result < 0 ? -1 : 0;
}
