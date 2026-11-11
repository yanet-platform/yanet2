#include "stages.h"

#include <errno.h>
#include <limits.h>

#include "common/container_of.h"
#include "dataplane_ut.inc.h"
#include "lib/dataplane/config/zone.h"
#include "lib/dataplane/packet/data.h"
#include "lib/dataplane/pipeline/pipeline.h"
#include "lib/dataplane/worker/pipeline_ready.h"
#include "lib/dataplane/worker/pipeline_round.h"
#include "lib/dataplane/worker/round.h"
#include "lib/utils/packet.h"
#include "mempool.h"

static int
dataplane_ut_stage_worker(
	struct dataplane_ut *ut,
	size_t worker_idx,
	struct dp_worker **worker,
	struct config_gen_ectx **context
) {
	if (worker_idx >= ut->dp_config->worker_count) {
		return -EINVAL;
	}
	struct dp_worker **workers = ADDR_OF(&ut->dp_config->workers);
	*worker = ADDR_OF(workers + worker_idx);
	*context = ATOMIC_ADDR_OF(&(*worker)->config_gen_ectx);
	struct dataplane_ut_stage_round *opened = ut->stage_rounds + worker_idx;
	if (opened->iteration == 0 || *context == NULL) {
		return -EINVAL;
	}
	struct cp_config_gen *generation = ADDR_OF(&(*context)->cp_config_gen);
	if (opened->generation != generation->gen ||
	    opened->iteration != *(*worker)->iterations) {
		return -ESTALE;
	}
	return 0;
}

int
dataplane_ut_begin_round(struct dataplane_ut *ut, size_t worker_idx) {
	if (worker_idx >= ut->dp_config->worker_count) {
		return -EINVAL;
	}
	pthread_mutex_lock(dp_config_external_round_lock(ut->dp_config));
	struct dp_worker **workers = ADDR_OF(&ut->dp_config->workers);
	struct dp_worker *worker = ADDR_OF(workers + worker_idx);
	struct worker_round round =
		worker_round_prepare(worker, ut->mock_time_ns);
	int result = -ENOENT;
	ut->stage_rounds[worker_idx] = (struct dataplane_ut_stage_round){0};
	if (round.config_gen_ectx != NULL) {
		ut->stage_rounds[worker_idx] =
			(struct dataplane_ut_stage_round){
				.generation = round.cp_config_gen->gen,
				.iteration = *worker->iterations,
			};
		result = 0;
	}
	pthread_mutex_unlock(dp_config_external_round_lock(ut->dp_config));
	return result;
}

int
dataplane_ut_run_stage(
	struct dataplane_ut *ut,
	const struct dataplane_ut_stage *stage,
	struct packet_list *input,
	struct dataplane_ut_round_result *result
) {
	packet_list_init(&result->output);
	packet_list_init(&result->drop);
	pthread_mutex_lock(dp_config_external_round_lock(ut->dp_config));
	struct dp_worker *worker = NULL;
	struct config_gen_ectx *context = NULL;
	int status = dataplane_ut_stage_worker(
		ut, stage->worker_idx, &worker, &context
	);
	struct device_ectx *device =
		status == 0
			? config_gen_ectx_get_device(context, stage->device_id)
			: NULL;
	if (status != 0 || device == NULL) {
		packet_list_concat(&result->drop, input);
		pthread_mutex_unlock(dp_config_external_round_lock(ut->dp_config
		));
		return status != 0 ? status : -EINVAL;
	}
	struct device_entry_ectx *entry = stage->output
						  ? device->abs_output_pipelines
						  : device->abs_input_pipelines;
	struct packet_front *front = &entry->schedule;
	struct packet *packet;
	while ((packet = packet_list_pop(input)) != NULL) {
		packet->flags |= stage->packet_flags;
		packet_front_input(front, packet);
	}
	worker_run_entry(worker, context, entry, &context->packet_front);
	worker_pipeline_round_process_ready(
		worker, context, &context->packet_front
	);
	packet_list_concat(&result->output, &context->packet_front.output);
	packet_list_concat(&result->drop, &context->packet_front.drop);
	packet_front_init(&context->packet_front);
	for (uint32_t idx = 0; idx < context->tx_bucket_count; ++idx) {
		packet_list_concat(
			&result->output, context->abs_tx_buckets + idx
		);
	}
	pthread_mutex_unlock(dp_config_external_round_lock(ut->dp_config));
	return 0;
}

int
dataplane_ut_set_allocation_budget(struct dataplane_ut *ut, int64_t budget) {
	if (budget < -1 || budget > TEST_MEMPOOL_DEFAULT_SIZE) {
		return -EINVAL;
	}
	pthread_mutex_lock(dp_config_external_round_lock(ut->dp_config));
	size_t outstanding = test_mempool_outstanding(ut->mempool);
	if (budget >= 0 &&
	    (uint64_t)budget + outstanding > TEST_MEMPOOL_DEFAULT_SIZE) {
		pthread_mutex_unlock(dp_config_external_round_lock(ut->dp_config
		));
		return -EINVAL;
	}
	ut->mempool->size = budget < 0 ? TEST_MEMPOOL_DEFAULT_SIZE
				       : outstanding + (uint32_t)budget;
	pthread_mutex_unlock(dp_config_external_round_lock(ut->dp_config));
	return 0;
}

int
dataplane_ut_stage_input_append(
	struct packet_list *input, const void *data, size_t size
) {
	if (size == 0 || size > UINT16_MAX - RTE_PKTMBUF_HEADROOM) {
		return -EINVAL;
	}
	struct rte_mbuf *mbuf =
		alloc_mbuf(RTE_PKTMBUF_HEADROOM, (uint16_t)size, 0);
	if (mbuf == NULL) {
		return -ENOMEM;
	}
	struct packet *packet = mbuf_to_packet(mbuf);
	memset(packet, 0, sizeof(*packet));
	packet->mbuf = mbuf;
	memcpy(rte_pktmbuf_mtod(mbuf, void *), data, size);
	int status = parse_packet(packet);
	if (status != 0) {
		free_packet(packet);
		return status;
	}
	packet_list_add(input, packet);
	return 0;
}
