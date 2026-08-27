#include "tx_stage.h"

#include "lib/dataplane/config/zone.h"
#include "lib/dataplane/packet/data.h"

// Hand one pipe's held packets over and account for the outcome.
static void
worker_tx_flush_pipe(
	struct worker_tx_pipe *tx_pipe,
	struct dp_worker *dp_worker,
	struct packet_list *failed
) {
	if (tx_pipe->batch_count == 0) {
		return;
	}

	struct rte_mbuf *rejected[WORKER_TX_BATCH_SIZE];
	size_t rejected_count = 0;
	size_t pushed =
		worker_tx_pipe_flush(tx_pipe, rejected, &rejected_count);

	*(dp_worker->remote_tx_count) += pushed;
	*(dp_worker->remote_tx_drops) += rejected_count;

	for (size_t idx = 0; idx < rejected_count; ++idx) {
		packet_list_add(failed, mbuf_to_packet(rejected[idx]));
	}
}

void
worker_tx_stage(
	struct worker_tx_connection *connections,
	uint32_t device_count,
	struct dp_worker *dp_worker,
	struct packet *packet,
	struct packet_list *failed
) {
	// A device this worker has no connections for is not a transmit
	// failure, so it joins the failures without counting one.
	if (packet->tx_device_id >= device_count) {
		packet_list_add(failed, packet);
		return;
	}

	struct worker_tx_connection *connection =
		connections + packet->tx_device_id;
	if (!connection->count) {
		*(dp_worker->remote_tx_drops) += 1;
		packet_list_add(failed, packet);
		return;
	}

	struct worker_tx_pipe *tx_pipe =
		connection->pipes + packet->hash % connection->count;

	// A full pipe goes now so this packet has somewhere to land; the rest
	// leave together at the end of the round.
	if (!worker_tx_pipe_stage(tx_pipe, packet_to_mbuf(packet))) {
		worker_tx_flush_pipe(tx_pipe, dp_worker, failed);
		worker_tx_pipe_stage(tx_pipe, packet_to_mbuf(packet));
	}
}

void
worker_tx_flush_all(
	struct worker_tx_connection *connections,
	uint32_t device_count,
	struct dp_worker *dp_worker,
	struct packet_list *failed
) {
	for (uint32_t conn_idx = 0; conn_idx < device_count; ++conn_idx) {
		struct worker_tx_connection *connection =
			connections + conn_idx;

		for (uint32_t pipe_idx = 0; pipe_idx < connection->count;
		     ++pipe_idx) {
			worker_tx_flush_pipe(
				connection->pipes + pipe_idx, dp_worker, failed
			);
		}
	}
}
