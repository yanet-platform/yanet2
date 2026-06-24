#include "dataplane.h"

#include <stdint.h>
#include <string.h>

#include "config.h"

#include "common/container_of.h"
#include "common/memory_address.h"

#include "lib/dataplane/device/device.h"
#include "lib/dataplane/module/packet_front.h"
#include "lib/dataplane/packet/data.h"
#include "lib/dataplane/packet/packet.h"
#include "lib/dataplane/pipeline/econtext.h"
#include "lib/dataplane/worker/worker.h"

#include <rte_mbuf.h>
#include <rte_memcpy.h>

#define TRAFGEN_NS_PER_SEC 1000000000ULL

#define TRAFGEN_MAX_BURST 1024

struct device_trafgen {
	struct device device;
};

// Build one packet from the frame at frame_idx and enqueue it for output.
//
// Returns 0 on success, -1 when the mbuf pool is exhausted (caller should stop
// the current burst) and 1 when the frame is malformed (caller should skip it).
static int
trafgen_emit_frame(
	struct dp_worker *dp_worker,
	struct cp_device_trafgen *config,
	struct packet_front *packet_front,
	uint32_t frame_idx
) {
	const uint8_t *frames = ADDR_OF(&config->frames);
	const uint32_t *offsets = ADDR_OF(&config->frame_offsets);
	const uint32_t *lengths = ADDR_OF(&config->frame_lengths);

	uint32_t len = lengths[frame_idx];

	// rte_pktmbuf_append() reserves tailroom with a 16-bit length, so a
	// larger frame would wrap and let the copy below overrun the mbuf. The
	// control plane rejects such frames at upload; guard the copy
	// regardless.
	if (len > UINT16_MAX) {
		return 1;
	}

	struct packet *packet = worker_packet_alloc(dp_worker);
	if (packet == NULL) {
		return -1;
	}

	struct rte_mbuf *mbuf = packet_to_mbuf(packet);
	char *data = rte_pktmbuf_append(mbuf, len);
	if (data == NULL) {
		worker_packet_free(packet);
		return 1;
	}
	rte_memcpy(data, frames + offsets[frame_idx], len);

	memset(packet, 0, sizeof(*packet));
	packet->mbuf = mbuf;
	packet->rx_device_id = dp_worker->device_id;
	packet->tx_device_id = dp_worker->device_id;

	if (parse_packet(packet)) {
		worker_packet_free(packet);
		return 1;
	}

	packet_front_output(packet_front, packet);
	return 0;
}

// Replay the loaded frames on the device input path, pacing to the target rate.
//
// The device registry force-polls every device input handler once per worker
// tick, so the generator sources fresh traffic even when no packet arrives from
// a NIC. The emitted packets land in packet_front->output and are demultiplexed
// into the device's configured input pipelines by the pipeline runtime. A
// per-worker token bucket accrues elapsed_ns * rate_pps and releases one packet
// per 1e9 * worker_count credit, which divides rate_pps evenly across all
// workers.
static void
trafgen_input_handle(
	struct dp_worker *dp_worker,
	struct device_ectx *device_ectx,
	struct packet_front *packet_front
) {
	struct cp_device_trafgen *config = container_of(
		ADDR_OF(&device_ectx->cp_device),
		struct cp_device_trafgen,
		cp_device
	);

	// Forward anything that arrived on this device unchanged; a generator
	// has no real RX, so this is normally empty.
	packet_list_concat(&packet_front->output, &packet_front->input);

	if (config->frame_count == 0 || config->rate_pps == 0 ||
	    config->worker_count == 0) {
		return;
	}

	struct trafgen_worker_state *state =
		ADDR_OF(&config->worker_states) + dp_worker->idx;

	uint64_t now = dp_worker->current_time;
	if (state->last_time == 0) {
		// First tick after (re)load: anchor the clock, emit nothing.
		state->last_time = now;
		return;
	}
	uint64_t elapsed = now > state->last_time ? now - state->last_time : 0;
	state->last_time = now;

	// One packet costs 1e9 * worker_count credit, so each worker emits
	// rate_pps / worker_count packets per second.
	uint64_t threshold = TRAFGEN_NS_PER_SEC * config->worker_count;
	uint64_t max_burst = TRAFGEN_MAX_BURST;
	uint64_t credit_cap = threshold * max_burst;

	uint64_t credit = state->credit + elapsed * config->rate_pps;
	if (credit > credit_cap) {
		credit = credit_cap;
	}

	uint64_t cursor = state->cursor;
	uint64_t emitted = 0;
	while (credit >= threshold && emitted < max_burst) {
		uint32_t frame_idx = cursor % config->frame_count;
		cursor += 1;

		int rc = trafgen_emit_frame(
			dp_worker, config, packet_front, frame_idx
		);
		if (rc < 0) {
			// Mbuf pool exhausted; keep the unspent credit.
			break;
		}
		credit -= threshold;
		emitted += 1;
	}

	state->cursor = cursor;
	state->credit = credit;
}

// Pass packets routed to this device for transmission straight through.
//
// A generator has no real TX queue, so the output path simply forwards anything
// a pipeline scheduled onto it without modification.
static void
trafgen_output_handle(
	struct dp_worker *dp_worker,
	struct device_ectx *device_ectx,
	struct packet_front *packet_front
) {
	(void)dp_worker;
	(void)device_ectx;

	packet_list_concat(&packet_front->output, &packet_front->input);
}

struct device *
new_device_trafgen() {
	struct device_trafgen *device =
		(struct device_trafgen *)malloc(sizeof(struct device_trafgen));

	if (device == NULL) {
		return NULL;
	}

	snprintf(
		device->device.name,
		sizeof(device->device.name),
		"%s",
		"trafgen"
	);
	device->device.input_handler = trafgen_input_handle;
	device->device.output_handler = trafgen_output_handle;

	return &device->device;
}
