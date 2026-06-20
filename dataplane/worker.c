/*
 * The file is under construction.
 * The first thought was  about providing device and queue identifiers into
 * worker. Worker processes infinite loop consisting of:
 *  - queue read
 *  - pipepline invocation
 *  - device routing and queue write
 * However the concept is not good as may be because of:
 *  - worker should know about device mapping
 *  - worker should implement logic for logical device routing as vlan/vxlan/
 *    bond/lagg/etc
 *  - multiple pipelines are hard to implement here as the are configured
 *    inside dataplane and shared across workers
 *  - inter-worker-communication to pass packets between numa-nodes
 *
 * New idea is:
 *  - dataplane is responsible for RX/TX and provides function callback to
 *    workers
 *  - concept of logical devices and multiple pipelines, each pipeline could
 *    be assigned to multiple logical devices whereas one logical device
 *    may have only one assigned pipeline. This will reduce pipeline
 *    configuration in case of virtual routers.
 *  - dataplane is responsible for L2 processing and routing packets between
 *    logical devices, merging and balancing laggs and so on
 *  - read and write callbacks should return packages with information about
 *    pipeline assigned to each packet.
 *
 * The only question is how pipeline should be attached to packet/mbuf
 * readings:
 *  - inside packet metadata
 *  - packets are clustered by pipeline and separate array with pipeline range
 *    provided
 *  - anything else
 */

#include "dataplane/time/clock.h"
#include "errors.h"
#include "yanet_build_config.h"

#include "worker.h"

#include "dataplane/dataplane.h"
#include "dataplane/device.h"

#include "lib/dataplane/config/zone.h"
#include "lib/dataplane/module/packet_front.h"
#include "lib/dataplane/packet/data.h"
#include "lib/dataplane/packet/packet.h"
#include "lib/dataplane/pipeline/econtext.h"
#include "lib/dataplane/pipeline/pipeline.h"
#include "lib/dataplane/time/clock.h"

#include "lib/controlplane/config/zone.h"
#include "lib/dataplane/worker/counters.h"
#include "lib/dataplane/worker/pipeline_round.h"

#include "common/data_pipe.h"
#include "logging/log.h"

#include <rte_ethdev.h>

static void
worker_read(struct dataplane_worker *worker, struct packet_list *packets) {
	struct worker_read_ctx *ctx = &worker->read_ctx;
	struct rte_mbuf *mbufs[ctx->read_size];

	uint16_t read = rte_eth_rx_burst(
		worker->port_id, worker->queue_id, mbufs, ctx->read_size
	);
	*(worker->dp_worker->rx_count) += read;
	if (worker->dp_worker->rx_bursts != NULL) {
		worker->dp_worker->rx_bursts[read] += 1;
	}

	for (uint32_t idx = 0; idx < read; ++idx) {
		struct packet *packet = mbuf_to_packet(mbufs[idx]);
		memset(packet, 0, sizeof(struct packet));
		// FIXME update packet fields
		packet->mbuf = mbufs[idx];

		packet->rx_device_id = worker->device_id;
		// Preserve device by default
		packet->tx_device_id = worker->device_id;

		if (parse_packet(packet)) {
			struct rte_mbuf *mbuf = packet_to_mbuf(packet);
			rte_pktmbuf_free(mbuf);
			continue;
		}
		packet_list_add(packets, packet);
	}
}

struct worker_push_ctx {
	struct dataplane_worker *worker;
	struct rte_mbuf *mbuf;
};

static size_t
worker_connection_push_cb(void **item, size_t count, void *data) {
	struct worker_push_ctx *push_ctx = (struct worker_push_ctx *)data;
	struct dataplane_worker *worker = push_ctx->worker;
	uint32_t num_mbufs =
		worker->config.num_mbufs ? worker->config.num_mbufs : 16384;

	if (count > 0) {
		uint32_t ref_cnt =
			rte_mbuf_refcnt_update(push_ctx->mbuf, 1) - 1;
		memcpy(item, &push_ctx->mbuf, sizeof(struct rte_mbuf *));

		uint32_t ofs = worker->pending_mbuf_stop % num_mbufs;
		worker->pending_mbufs[ofs].mbuf = push_ctx->mbuf;
		worker->pending_mbufs[ofs].ref_cnt = ref_cnt;
		++worker->pending_mbuf_stop;

		return 1;
	}
	return 0;
}

static size_t
worker_rx_pipe_pop_cb(void **item, size_t count, void *data) {
	struct dataplane_worker *worker = (struct dataplane_worker *)data;
	struct rte_mbuf **mbufs = (struct rte_mbuf **)item;

	(*worker->dp_worker->remote_rx_count) += count;

	size_t written = rte_eth_tx_burst(
		worker->port_id, worker->queue_id, mbufs, count
	);
	*(worker->dp_worker->tx_count) += written;

	for (size_t idx = written; idx < count; ++idx) {
		rte_pktmbuf_free(mbufs[idx]);
	}

	return count;
}

static size_t
worker_connection_free_cb(void **item, size_t count, void *data) {
	(void)item;
	(void)data;

	return count;
}

/*
 * FIXME: the function below sends a packet to a different worker
 * using corresponding data pipe so the routine name might be confusing.
 */
static int
worker_send_to_port(struct dataplane_worker *worker, struct packet *packet) {
	struct worker_tx_connection *tx_conn =
		worker->write_ctx.tx_connections + packet->tx_device_id;

	if (!tx_conn->count) {
		return -1;
	}

	uint32_t num_mbufs =
		worker->config.num_mbufs ? worker->config.num_mbufs : 16384;
	if (worker->pending_mbuf_stop - worker->pending_mbuf_stop >=
	    num_mbufs) {
		// To many pending mbufs
		return -1;
	}

	struct worker_push_ctx push_ctx = {
		.worker = worker,
		.mbuf = packet_to_mbuf(packet),
	};

	if (data_pipe_item_push(
		    tx_conn->pipes + packet->hash % tx_conn->count,
		    worker_connection_push_cb,
		    &push_ctx
	    ) != 1) {
		return -1;
	}

	return 0;
}

static void
worker_collect_from_port(struct dataplane_worker *worker) {
	for (uint32_t conn_idx = 0; conn_idx < worker->dataplane->device_count;
	     ++conn_idx) {
		struct worker_tx_connection *tx_conn =
			worker->write_ctx.tx_connections + conn_idx;
		for (uint32_t pipe_idx = 0; pipe_idx < tx_conn->count;
		     ++pipe_idx) {
			data_pipe_item_free(
				tx_conn->pipes + pipe_idx,
				worker_connection_free_cb,
				worker
			);
		}
	}
}

static void
worker_submit_burst(
	struct dataplane_worker *worker,
	struct rte_mbuf **mbufs,
	uint16_t count,
	struct packet_list *failed
) {
	uint16_t written = rte_eth_tx_burst(
		worker->port_id, worker->queue_id, mbufs, count
	);

	*(worker->dp_worker->tx_count) += written;

	for (uint16_t idx = written; idx < count; ++idx) {
		packet_list_add(failed, mbuf_to_packet(mbufs[idx]));
	}
}

static void
worker_write(struct dataplane_worker *worker, struct packet_list *packets) {
	struct dp_config *dp_config = worker->instance->dp_config;

	struct packet_list failed;
	packet_list_init(&failed);

	struct worker_write_ctx *ctx = &worker->write_ctx;
	struct rte_mbuf *mbufs[ctx->write_size];

	uint16_t to_write = 0;

	struct packet *packet;
	while ((packet = packet_list_pop(packets)) != NULL) {
		if (to_write == ctx->write_size) {
			worker_submit_burst(worker, mbufs, to_write, &failed);
			to_write = 0;
		}

		if (packet->tx_device_id >=
		    dp_config->dp_topology.device_count) {
			packet_list_add(&failed, packet);
			continue;
		}

		if (packet->tx_device_id == worker->device_id) {
			mbufs[to_write] = packet_to_mbuf(packet);
			++to_write;
		} else {
			if (worker_send_to_port(worker, packet)) {
				packet_list_add(&failed, packet);
			} else {
				*(worker->dp_worker->remote_tx_count) += 1;
			}
		}
	}

	if (to_write > 0) {
		worker_submit_burst(worker, mbufs, to_write, &failed);
	}

	struct packet_list sent;
	packet_list_init(&sent);
	worker_collect_from_port(worker);

	// FIXME: handle pending mbufs for each data pipe
	uint32_t num_mbufs =
		worker->config.num_mbufs ? worker->config.num_mbufs : 16384;
	uint64_t pending_mbuf_stop = worker->pending_mbuf_stop;
	while (worker->pending_mbuf_start < pending_mbuf_stop) {
		uint32_t ofs = worker->pending_mbuf_start % num_mbufs;
		struct worker_pending_mbuf pending_mbuf =
			worker->pending_mbufs[ofs];

		if (rte_mbuf_refcnt_read(pending_mbuf.mbuf) >
		    pending_mbuf.ref_cnt) {
			uint32_t ofs = worker->pending_mbuf_stop % num_mbufs;
			worker->pending_mbufs[ofs] = pending_mbuf;
			++worker->pending_mbuf_stop;
		} else {
			rte_pktmbuf_free(pending_mbuf.mbuf);
		}

		++worker->pending_mbuf_start;
	}
	// Value overflow protection
	if (worker->pending_mbuf_start > num_mbufs) {
		worker->pending_mbuf_start -= num_mbufs;
		worker->pending_mbuf_stop -= num_mbufs;
	}

	packet_list_concat(packets, &failed);

	for (uint32_t pipe_idx = 0; pipe_idx < ctx->rx_pipe_count; ++pipe_idx) {
		data_pipe_item_pop(
			ctx->rx_pipes + pipe_idx, worker_rx_pipe_pop_cb, worker
		);
	}
}

static void
worker_loop_round(struct dataplane_worker *worker) {
	// Initialize current worker time
	// on the start of loop round
	{
		struct dp_worker *dp_worker = worker->dp_worker;
		dp_worker->current_time =
			dataplane_time_ns_fn(&dp_worker->clock);
	}

	struct cp_config *cp_config = worker->instance->cp_config;
	struct cp_config_gen *cp_config_gen =
		ADDR_OF(&cp_config->cp_config_gen);
	struct config_gen_ectx *config_gen_ectx =
		ADDR_OF(&cp_config_gen->config_gen_ectx);

	__atomic_store_n(
		&worker->dp_worker->gen, cp_config_gen->gen, __ATOMIC_RELEASE
	);
	*worker->dp_worker->iterations += 1;

	struct packet_front packet_front;
	packet_front_init(&packet_front);

	worker_read(worker, &packet_front.pending_input);

	if (config_gen_ectx == NULL) {
		worker_write(worker, &packet_front.drop);

		packet_list_concat(
			&packet_front.drop, &packet_front.pending_input
		);

		dataplane_drop_packets(worker->dataplane, &packet_front.drop);

		return;
	}

	worker_pipeline_round(
		worker->dp_worker, cp_config_gen, config_gen_ectx, &packet_front
	);

	worker_write(worker, &packet_front.output);

	/*
	 * `output_packets` now contains failed-to-transmit packets which
	 * should be freed.
	 */
	packet_list_concat(&packet_front.drop, &packet_front.output);

	dataplane_drop_packets(worker->dataplane, &packet_front.drop);
}

static void *
worker_thread_start(void *arg) {
	struct dataplane_worker *worker = (struct dataplane_worker *)arg;

	while (1) {
		worker_loop_round(worker);
	}

	return NULL;
}

int
dataplane_worker_init(
	struct dataplane *dataplane,
	struct dataplane_device *device,
	struct dataplane_worker *worker,
	int queue_id,
	struct dataplane_device_worker_config *config
) {
	// FIXME: free resources on error
	LOG(DEBUG,
	    "initialize worker core=%u, instance=%u for port_id=%u",
	    config->core_id,
	    config->instance_id,
	    device->port_id);
	worker->dataplane = dataplane;
	worker->instance = dataplane->instances + config->instance_id;
	worker->device = device;
	worker->device_id = device->device_id;
	worker->port_id = device->port_id;
	worker->queue_id = queue_id;
	worker->config = *config;

	struct dp_config *dp_config = worker->instance->dp_config;
	struct dp_worker *dp_worker = (struct dp_worker *)memory_balloc(
		&dp_config->memory_context, sizeof(struct dp_worker)
	);
	if (dp_worker == NULL) {
		return -1;
	}
	memset(dp_worker, 0, sizeof(struct dp_worker));
	dp_worker->idx = dp_config->worker_count;

	// Init worker clock
	int init_clock_result = tsc_clock_init(&dp_worker->clock);
	if (init_clock_result != 0) {
		return -1;
	}

	worker->dp_worker = dp_worker;
	struct dp_worker **new_workers = (struct dp_worker **)memory_balloc(
		&dp_config->memory_context,
		sizeof(struct dp_worker **) * (dp_config->worker_count + 1)
	);
	if (new_workers == NULL) {
		memory_bfree(
			&dp_config->memory_context,
			dp_worker,
			sizeof(struct dp_worker)
		);
		return -1;
	}
	struct dp_worker **old_workers = ADDR_OF(&dp_config->workers);
	for (uint64_t idx = 0; idx < dp_config->worker_count; ++idx) {
		SET_OFFSET_OF(new_workers + idx, ADDR_OF(old_workers + idx));
	}

	SET_OFFSET_OF(new_workers + dp_config->worker_count, dp_worker);
	// FIXME workers should be set up after device initialization
	SET_OFFSET_OF(&dp_config->workers, new_workers);
	memory_bfree(
		&dp_config->memory_context,
		old_workers,
		sizeof(struct dp_worker **) * dp_config->worker_count
	);
	dp_config->worker_count += 1;

	worker->read_ctx.read_size = WORKER_RX_BURST_SIZE;
	worker->write_ctx.write_size = 32;
	worker->write_ctx.rx_pipes = NULL;
	dp_worker->core_id = config->core_id;
	dp_worker->device_id = worker->device_id;
	dp_worker->queue_id = queue_id;
	dp_worker->rx_burst_size = WORKER_RX_BURST_SIZE;

	// Initialize device rx and tx queue
	if (rte_eth_tx_queue_setup(
		    device->port_id, queue_id, 4096, config->instance_id, NULL
	    )) {
		LOG(ERROR,
		    "failed to setup TX queue for port id=%u instance=%u",
		    device->port_id,
		    config->instance_id);
		return -1;
	}

	char mempool_name[80];
	snprintf(
		mempool_name,
		sizeof(mempool_name) - 1,
		"wrk_rx_pool_%d_%d",
		device->port_id,
		queue_id
	);

	uint32_t num_mbufs = config->num_mbufs ? config->num_mbufs : 16384;

	worker->pending_mbufs = (struct worker_pending_mbuf *)memory_balloc(
		&dp_config->memory_context,
		sizeof(struct worker_pending_mbuf) * num_mbufs
	);
	if (worker->pending_mbufs == NULL) {
		LOG(ERROR, "failed to create worker pending list");
		return -1;
	}
	worker->pending_mbuf_start = 0;
	worker->pending_mbuf_stop = 0;

	worker->rx_mempool = rte_mempool_create(
		mempool_name,
		num_mbufs,
		MBUF_MAX_SIZE,
		0,
		sizeof(struct rte_pktmbuf_pool_private),
		rte_pktmbuf_pool_init,
		NULL,
		rte_pktmbuf_init,
		NULL,
		config->instance_id,
		MEMPOOL_F_SP_PUT | MEMPOOL_F_SC_GET
	);
	if (worker->rx_mempool == NULL) {
		LOG(ERROR, "failed to create worker rx pool %s", mempool_name);
		return -1;
	}
	dp_worker->rx_mempool = worker->rx_mempool;

	if (rte_eth_rx_queue_setup(
		    device->port_id,
		    queue_id,
		    4096,
		    config->instance_id,
		    NULL,
		    worker->rx_mempool
	    )) {
		LOG(ERROR,
		    "failed to setup RX queue for port id=%u instance=%u",
		    device->port_id,
		    config->instance_id);
		goto error_mempool;
	}

	// Allocate connection data for each dataplane device
	worker->write_ctx.tx_connections = (struct worker_tx_connection *)
		malloc(sizeof(struct worker_tx_connection) *
		       dataplane->device_count);
	if (worker->write_ctx.tx_connections == NULL) {
		goto error_mempool;
	}

	memset(worker->write_ctx.tx_connections,
	       0,
	       sizeof(struct worker_tx_connection) * dataplane->device_count);

	// Initialize zero rx connections
	worker->write_ctx.rx_pipe_count = 0;

	// Prepare counter registry
	counter_registry_init(
		&dp_config->worker_counters, &dp_config->memory_context, 0
	);

	if (worker_counters_register(dp_config)) {
		return -1;
	}

	return 0;

error_mempool:
	rte_mempool_free(worker->rx_mempool);

	return -1;
}

int
dataplane_worker_start(struct dataplane_worker *worker) {

	struct dp_worker *dp_worker = worker->dp_worker;
	struct dp_config *dp_config = worker->instance->dp_config;
	// FIXME: do not use hard-coded counter identifiers
	dp_worker->iterations = counter_get_address(
		0, dp_worker->idx, ADDR_OF(&dp_config->worker_counter_storage)
	);

	dp_worker->rx_count =
		counter_get_address(
			1,
			dp_worker->idx,
			ADDR_OF(&dp_config->worker_counter_storage)
		) +
		0;
	dp_worker->rx_size = counter_get_address(
				     1,
				     dp_worker->idx,
				     ADDR_OF(&dp_config->worker_counter_storage)
			     ) +
			     1;

	dp_worker->tx_count =
		counter_get_address(
			2,
			dp_worker->idx,
			ADDR_OF(&dp_config->worker_counter_storage)
		) +
		0;
	dp_worker->tx_size = counter_get_address(
				     2,
				     dp_worker->idx,
				     ADDR_OF(&dp_config->worker_counter_storage)
			     ) +
			     1;

	dp_worker->remote_rx_count =
		counter_get_address(
			3,
			dp_worker->idx,
			ADDR_OF(&dp_config->worker_counter_storage)
		) +
		0;

	dp_worker->remote_tx_count =
		counter_get_address(
			4,
			dp_worker->idx,
			ADDR_OF(&dp_config->worker_counter_storage)
		) +
		0;
	dp_worker->rx_bursts = counter_get_address(
		5, dp_worker->idx, ADDR_OF(&dp_config->worker_counter_storage)
	);

	pthread_attr_t wrk_th_attr;
	pthread_attr_init(&wrk_th_attr);

	cpu_set_t mask;
	CPU_ZERO(&mask);
	CPU_SET(worker->config.core_id, &mask);

	pthread_attr_setaffinity_np(&wrk_th_attr, sizeof(cpu_set_t), &mask);

	pthread_create(
		&worker->thread_id, &wrk_th_attr, worker_thread_start, worker
	);

	pthread_attr_destroy(&wrk_th_attr);

	return 0;
}

void
dataplane_worker_stop(struct dataplane_worker *worker) {
	pthread_join(worker->thread_id, NULL);
}
