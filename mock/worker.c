#include "worker.h"
#include "common/memory_address.h"

#include "../../lib/controlplane/config/zone.h"
#include "dataplane/config/zone.h"
#include "dataplane/pipeline/pipeline.h"
#include "dataplane/time/clock.h"
#include "lib/dataplane/module/packet_front.h"
#include "packet.h"

////////////////////////////////////////////////////////////////////////////////

// Mock dp worker library.

/*
FIXME: confict with definition inside lib/dataplane/worker
struct packet *
worker_clone_packet(struct dp_worker *dp_worker, struct packet *packet) {
	(void)dp_worker;
	struct rte_mbuf *src_mbuf = packet->mbuf;
	size_t data_len = rte_pktmbuf_data_len(src_mbuf);
	size_t buf_len = RTE_PKTMBUF_HEADROOM + data_len;
	const size_t align = alignof(struct rte_mbuf);
	if (buf_len % align != 0) {
		buf_len += align - buf_len % align;
	}
	size_t total_size = sizeof(struct rte_mbuf) + buf_len;
	struct rte_mbuf *mbuf = aligned_alloc(align, total_size);
	if (mbuf == NULL) {
		return NULL;
	}

	// Initialize the mbuf structure
	memset(mbuf, 0, sizeof(struct rte_mbuf));
	mbuf->buf_addr = ((char *)mbuf) + sizeof(struct rte_mbuf);
	mbuf->buf_len = buf_len;
	mbuf->data_off = RTE_PKTMBUF_HEADROOM;
	mbuf->refcnt = 1;
	mbuf->nb_segs = 1;
	mbuf->port = src_mbuf->port;
	mbuf->next = NULL;

	// Copy layer length fields explicitly
	mbuf->l2_len = src_mbuf->l2_len;
	mbuf->l3_len = src_mbuf->l3_len;
	mbuf->l4_len = src_mbuf->l4_len;

	struct packet *packet_clone = mbuf_to_packet(mbuf);
	rte_memcpy(packet_clone, packet, sizeof(struct packet));
	packet_clone->mbuf = mbuf;
	packet_clone->next = NULL;

	mbuf_copy(packet_clone->mbuf, src_mbuf);
	return packet_clone;
}
*/

////////////////////////////////////////////////////////////////////////////////

void
yanet_worker_mock_handle_packets(
	struct yanet_worker_mock *worker,
	struct packet_list *input_packets,
	struct packet_handle_result *out_result
) {
	// initialize worker time
	{
		struct dp_worker *dp_worker = &worker->dp_worker;
		dp_worker->current_time =
			tsc_clock_get_time_ns(&dp_worker->clock);
	}

	struct cp_config *cp_config = worker->cp_config;
	struct cp_config_gen *cp_config_gen =
		ADDR_OF(&cp_config->cp_config_gen);
	struct config_gen_ectx *config_gen_ectx =
		ADDR_OF(&cp_config_gen->config_gen_ectx);

	// do not update worker gen as it set to very big number previously

	packet_list_init(&out_result->output_packets);
	packet_list_init(&out_result->drop_packets);

	if (config_gen_ectx == NULL) {
		packet_list_concat(&out_result->drop_packets, input_packets);
		packet_list_init(input_packets);
	}

	struct packet_front packet_front;
	packet_front_init(&packet_front);

	packet_list_concat(&packet_front.pending_input, input_packets);

	uint64_t device_count =
		cp_config_gen->device_registry.registry.capacity;

	while (1) {

		struct packet_front schedule_input[device_count];
		for (uint64_t idx = 0; idx < device_count; ++idx)
			packet_front_init(schedule_input + idx);

		struct packet_front schedule_output[device_count];
		for (uint64_t idx = 0; idx < device_count; ++idx)
			packet_front_init(schedule_output + idx);

		struct packet *packet;

		int empty = 1;

		while ((packet = packet_list_pop(&packet_front.pending_input)
		       ) != NULL) {
			empty = 0;
			packet_front_output(
				schedule_input + packet->tx_device_id, packet
			);
		}

		while ((packet = packet_list_pop(&packet_front.pending_output)
		       ) != NULL) {
			empty = 0;
			packet_front_output(
				schedule_output + packet->tx_device_id, packet
			);
		}

		if (empty)
			break;

		struct device_ectx **devices = config_gen_ectx->devices;

		for (uint64_t idx = 0; idx < device_count; ++idx) {
			if (packet_list_first(&schedule_input[idx].output) ==
			    NULL)
				continue;

			struct device_ectx *device_ectx =
				ADDR_OF(devices + idx);

			device_ectx_process_input(
				&worker->dp_worker,
				device_ectx,
				schedule_input + idx
			);

			packet_front_merge(&packet_front, schedule_input + idx);
		}

		for (uint64_t idx = 0; idx < device_count; ++idx) {
			if (packet_list_first(&schedule_output[idx].output) ==
			    NULL)
				continue;

			struct device_ectx *device_ectx =
				ADDR_OF(devices + idx);

			device_ectx_process_output(
				&worker->dp_worker,
				device_ectx,
				schedule_output + idx
			);

			packet_front_merge(
				&packet_front, schedule_output + idx
			);
		}
	}

	packet_list_concat(&out_result->drop_packets, &packet_front.drop);
	packet_list_init(&packet_front.drop);
	packet_list_concat(&out_result->output_packets, &packet_front.output);
	packet_list_init(&packet_front.output);
}
