#include "ringtest_writer.h"

#include <pthread.h>
#include <stdatomic.h>
#include <stdlib.h>
#include <string.h>

#include "common/memory_address.h"
#include "common/ring.h"

#include "lib/controlplane/agent/agent.h"
#include "lib/controlplane/config/cp_object.h"
#include "lib/controlplane/config/zone.h"

#include "objects/ring/api/ring_object.h"

// One writer thread that commits records to a worker's ring at full speed.
//
// The Go scheduler does not pace it. The ring publishes records by its own
// publish batch. At the end the writer publishes the last records once.
struct ringtest_stress {
	struct ring_worker *ring;
	uint8_t *data;
	uint64_t records;
	_Atomic uint64_t written;
	_Atomic int done;
	pthread_t thread;
};

struct cp_object *
ringtest_lookup_ring(struct agent *agent, const char *name) {
	struct cp_config *cp_config = ADDR_OF(&agent->cp_config);
	cp_config_lock(cp_config);
	struct cp_config_gen *gen = ADDR_OF(&cp_config->cp_config_gen);
	struct cp_object *object =
		cp_config_gen_lookup_object(gen, RING_OBJECT_TYPE, name);
	cp_config_unlock(cp_config);
	return object;
}

struct cp_object_registry *
ringtest_hold(struct agent *agent, const char *name, yanet_error **err) {
	struct cp_object *object = ringtest_lookup_ring(agent, name);
	if (object == NULL) {
		yanet_error_add(err, "ring %s is not published", name);
		return NULL;
	}

	struct cp_object_registry *registry = malloc(sizeof(*registry));
	if (registry == NULL) {
		yanet_error_add(err, "failed to allocate a reference registry");
		return NULL;
	}

	// The registry calls check that the caller holds the lock of the
	// registry's owner. This helper takes the agent's lock, so the owner
	// must be the agent's config, not NULL.
	struct cp_config *cp_config = ADDR_OF(&agent->cp_config);
	if (cp_object_registry_init(
		    &agent->memory_context, cp_config, registry, err
	    ) != 0) {
		free(registry);
		return NULL;
	}

	// Production code changes the registry under the same lock, so this
	// reference cannot race with a publish or a delete of the same object.
	cp_config_lock(cp_config);
	int rc = cp_object_registry_upsert(
		registry, RING_OBJECT_TYPE, name, object, err
	);
	if (rc != 0) {
		cp_object_registry_fini(registry);
	}
	cp_config_unlock(cp_config);
	if (rc != 0) {
		free(registry);
		return NULL;
	}
	return registry;
}

void
ringtest_release(struct agent *agent, struct cp_object_registry *registry) {
	struct cp_config *cp_config = ADDR_OF(&agent->cp_config);
	cp_config_lock(cp_config);
	cp_object_registry_fini(registry);
	cp_config_unlock(cp_config);
	free(registry);
}

void
ringtest_set_positions(
	struct ring_worker *ring, uint64_t write_idx, uint64_t readable_idx
) {
	ring_worker_set_positions(ring, write_idx, readable_idx);
}

void
ringtest_corrupt_total_len(
	struct ring_worker *ring,
	uint8_t *data,
	uint64_t logical_offset,
	uint32_t total_len
) {
	uint8_t bytes[sizeof(total_len)];
	memcpy(bytes, &total_len, sizeof(bytes));
	for (uint64_t idx = 0; idx < sizeof(bytes); idx++) {
		data[(logical_offset + idx) & ring->local.mask] = bytes[idx];
	}
}

int64_t
ringtest_commit_record(
	struct ring_worker *ring,
	uint8_t *data,
	const uint8_t *payload,
	uint32_t payload_len
) {
	uint32_t total_len = RING_RECORD_FRAME_SIZE + payload_len;
	uint32_t seqno = ring->local.next_seqno;
	if (ring_worker_prepare(ring, data, total_len) != 0) {
		return -1;
	}
	if (payload_len != 0) {
		ring_worker_write(
			ring, data, RING_RECORD_FRAME_SIZE, payload, payload_len
		);
	}
	ring_worker_commit(ring, total_len);
	return seqno;
}

void
ringtest_publish(struct ring_worker *ring) {
	ring_worker_publish(ring);
}

static void *
ringtest_stress_run(void *arg) {
	struct ringtest_stress *s = arg;
	uint32_t buf[64];
	uint64_t committed = 0;

	for (; committed < s->records; committed++) {
		uint32_t seqno = s->ring->local.next_seqno;
		uint32_t len = ringtest_stress_len(seqno);
		for (uint32_t k = 0; k < len / 4; k++) {
			buf[k] = ringtest_stress_word(seqno, k);
		}
		uint32_t total = RING_RECORD_FRAME_SIZE + len;
		if (ring_worker_prepare(s->ring, s->data, total) != 0) {
			break;
		}
		ring_worker_write(
			s->ring,
			s->data,
			RING_RECORD_FRAME_SIZE,
			(const uint8_t *)buf,
			len
		);
		ring_worker_commit(s->ring, total);
	}
	ring_worker_publish(s->ring);
	atomic_store_explicit(&s->written, committed, memory_order_relaxed);
	atomic_store(&s->done, 1);
	return NULL;
}

struct ringtest_stress *
ringtest_stress_start(
	struct ring_worker *ring, uint8_t *data, uint64_t records
) {
	struct ringtest_stress *s = calloc(1, sizeof(*s));
	if (s == NULL) {
		return NULL;
	}
	s->ring = ring;
	s->data = data;
	s->records = records;
	if (pthread_create(&s->thread, NULL, ringtest_stress_run, s) != 0) {
		free(s);
		return NULL;
	}
	return s;
}

int
ringtest_stress_done(struct ringtest_stress *stress) {
	return atomic_load(&stress->done);
}

uint64_t
ringtest_stress_written(struct ringtest_stress *stress) {
	return atomic_load(&stress->written);
}

void
ringtest_stress_join(struct ringtest_stress *stress) {
	pthread_join(stress->thread, NULL);
	free(stress);
}
