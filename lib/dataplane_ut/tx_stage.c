#include "tx_stage.h"

#include <stdlib.h>
#include <string.h>

#include <rte_mbuf.h>

#include "lib/dataplane/config/zone.h"
#include "lib/dataplane/packet/data.h"
#include "lib/dataplane/packet/packet.h"
#include "lib/dataplane/worker/tx_stage.h"
#include "lib/dataplane_ut/mempool.h"

struct dataplane_ut_tx_stage {
	struct rte_mempool *mempool;

	struct worker_tx_connection *connections;
	struct worker_tx_pipe *pipes;
	uint32_t device_count;
	uint32_t pipes_per_device;

	struct dp_worker dp_worker;
	uint64_t tx_count;
	uint64_t tx_drops;

	struct packet_list failed;
};

struct dataplane_ut_tx_stage *
dataplane_ut_tx_stage_new(uint32_t device_count, uint32_t pipes_per_device) {
	struct dataplane_ut_tx_stage *fixture =
		(struct dataplane_ut_tx_stage *)calloc(1, sizeof(*fixture));
	if (fixture == NULL) {
		return NULL;
	}

	fixture->mempool = test_mempool_create();
	if (fixture->mempool == NULL) {
		free(fixture);
		return NULL;
	}

	fixture->device_count = device_count;
	fixture->pipes_per_device = pipes_per_device;
	fixture->connections = (struct worker_tx_connection *)calloc(
		device_count, sizeof(struct worker_tx_connection)
	);
	fixture->pipes = (struct worker_tx_pipe *)calloc(
		(size_t)device_count * pipes_per_device + 1,
		sizeof(struct worker_tx_pipe)
	);
	if (fixture->connections == NULL || fixture->pipes == NULL) {
		dataplane_ut_tx_stage_free(fixture);
		return NULL;
	}

	// Count pipes as they come up, never before: a failed pipe leaves its
	// own storage released, so teardown must not visit it.
	for (uint32_t device = 0; device < device_count; ++device) {
		fixture->connections[device].count = 0;
		fixture->connections[device].pipes =
			fixture->pipes + (size_t)device * pipes_per_device;

		for (uint32_t pipe = 0; pipe < pipes_per_device; ++pipe) {
			if (worker_tx_pipe_init(
				    fixture->connections[device].pipes + pipe
			    )) {
				dataplane_ut_tx_stage_free(fixture);
				return NULL;
			}
			++fixture->connections[device].count;
		}
	}

	fixture->dp_worker.remote_tx_count = &fixture->tx_count;
	fixture->dp_worker.remote_tx_drops = &fixture->tx_drops;
	packet_list_init(&fixture->failed);

	return fixture;
}

void
dataplane_ut_tx_stage_free(struct dataplane_ut_tx_stage *fixture) {
	if (fixture == NULL) {
		return;
	}

	if (fixture->pipes != NULL && fixture->connections != NULL) {
		for (uint32_t device = 0; device < fixture->device_count;
		     ++device) {
			for (uint32_t pipe = 0;
			     pipe < fixture->connections[device].count;
			     ++pipe) {
				worker_tx_pipe_fini(
					fixture->connections[device].pipes +
					pipe
				);
			}
		}
	}

	free(fixture->pipes);
	free(fixture->connections);
	if (fixture->mempool != NULL) {
		test_mempool_free(fixture->mempool);
	}
	free(fixture);
}

struct packet *
dataplane_ut_tx_stage_packet(
	struct dataplane_ut_tx_stage *fixture,
	uint16_t tx_device_id,
	uint32_t hash
) {
	struct rte_mbuf *mbuf = rte_pktmbuf_alloc(fixture->mempool);
	if (mbuf == NULL) {
		return NULL;
	}

	struct packet *packet = mbuf_to_packet(mbuf);
	memset(packet, 0, sizeof(*packet));
	packet->mbuf = mbuf;
	packet->tx_device_id = tx_device_id;
	packet->hash = hash;

	return packet;
}

void
dataplane_ut_tx_stage_offer(
	struct dataplane_ut_tx_stage *fixture, struct packet *packet
) {
	worker_tx_stage(
		fixture->connections,
		fixture->device_count,
		&fixture->dp_worker,
		packet,
		&fixture->failed
	);
}

void
dataplane_ut_tx_stage_flush(struct dataplane_ut_tx_stage *fixture) {
	worker_tx_flush_all(
		fixture->connections,
		fixture->device_count,
		&fixture->dp_worker,
		&fixture->failed
	);
}

uint32_t
dataplane_ut_tx_stage_held(
	struct dataplane_ut_tx_stage *fixture, uint32_t device, uint32_t pipe
) {
	return fixture->connections[device].pipes[pipe].batch_count;
}

uint64_t
dataplane_ut_tx_stage_placed(
	struct dataplane_ut_tx_stage *fixture, uint32_t device, uint32_t pipe
) {
	return fixture->connections[device].pipes[pipe].pending_stop;
}

uint64_t
dataplane_ut_tx_stage_tx_count(struct dataplane_ut_tx_stage *fixture) {
	return fixture->tx_count;
}

uint64_t
dataplane_ut_tx_stage_tx_drops(struct dataplane_ut_tx_stage *fixture) {
	return fixture->tx_drops;
}

size_t
dataplane_ut_tx_stage_failed_count(struct dataplane_ut_tx_stage *fixture) {
	size_t count = 0;
	for (struct packet *packet = packet_list_first(&fixture->failed);
	     packet != NULL;
	     packet = packet->next) {
		++count;
	}
	return count;
}

struct packet *
dataplane_ut_tx_stage_failed_at(
	struct dataplane_ut_tx_stage *fixture, size_t idx
) {
	struct packet *packet = packet_list_first(&fixture->failed);
	while (packet != NULL && idx-- > 0) {
		packet = packet->next;
	}
	return packet;
}
