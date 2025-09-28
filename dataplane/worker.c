/*
 * The file is under construction.
 * The first tought was  about provinding device and queue identifiers into
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
 *  - dataplane is reposnsible for L2 processing and routing packets between
 *    logical devices, merging and balancing laggs and so on
 *  - read and writ callbacks should return packages with information about
 *    pipeline assigned to each packet.
 *
 * The only question is how pipeline should be attached to packet/mbuf
 * readings:
 *  - inside packet metadata
 *  - packets are clustered by pipeline and separate array with pipeline range
 *    provided
 *  - anything else
 */

#include "yanet_build_config.h"

#include "worker.h"

#include "dataplane/dataplane.h"
#include "dataplane/device.h"

#include "dataplane/pipeline/pipeline.h"

#include "controlplane/config/zone.h"
#include "dataplane/config/zone.h"

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

	for (uint32_t idx = 0; idx < read; ++idx) {
		struct packet *packet = mbuf_to_packet(mbufs[idx]);
		memset(packet, 0, sizeof(struct packet));
		// FIXME update packet fields
		packet->mbuf = mbufs[idx];

		packet->rx_device_id = worker->device_id;
		// Preserve device by default
		packet->tx_device_id = worker->device_id;

		parse_packet(packet);
		packet_list_add(packets, packet);
	}
}

static size_t
worker_connection_push_cb(void **item, size_t count, void *data) {
	if (count > 0) {
		struct packet *packet = (struct packet *)data;
		struct rte_mbuf *mbuf = packet_to_mbuf(packet);
		rte_mbuf_refcnt_update(mbuf, 1);
		memcpy(item, &data, sizeof(struct packet *));
		return 1;
	}
	return 0;
}

static size_t
worker_rx_pipe_pop_cb(void **item, size_t count, void *data) {
	struct dataplane_worker *worker = (struct dataplane_worker *)data;
	struct packet **packets = (struct packet **)item;

	(*worker->dp_worker->remote_rx_count) += count;

	struct rte_mbuf *mbufs[count];
	for (size_t idx = 0; idx < count; ++idx) {
		mbufs[idx] = packet_to_mbuf(packets[idx]);
	}

	size_t written = rte_eth_tx_burst(
		worker->port_id, worker->queue_id, mbufs, count
	);
	*(worker->dp_worker->tx_count) += written;

	for (size_t idx = 0; idx < written; ++idx) {
		packets[idx]->tx_result = 0;
	}

	for (size_t idx = written; idx < count; ++idx) {
		packets[idx]->tx_result = -1;
		rte_pktmbuf_free(mbufs[idx]);
	}

	return count;
}

static size_t
worker_connection_free_cb(void **item, size_t count, void *data) {
	struct packet_list *sent = (struct packet_list *)data;

	for (size_t idx = 0; idx < count; ++idx) {
		struct packet *packet = ((struct packet **)item)[idx];
		packet_list_add(sent, packet);
	}
	return count;
}

/*
 * FIXME: the function bellow sends a packet to a different worker
 * using corresponding data pipe so the routine name might be confusing.
 */
static int
worker_send_to_port(struct worker_write_ctx *ctx, struct packet *packet) {
	struct worker_tx_connection *tx_conn =
		ctx->tx_connections + packet->tx_device_id;

	if (!tx_conn->count) {
		LOG(ERROR, "no available data pipe for the port");
		return -1;
	}

	if (data_pipe_item_push(
		    tx_conn->pipes + packet->hash % tx_conn->count,
		    worker_connection_push_cb,
		    packet
	    ) != 1) {
		LOG(ERROR, "data pipe is full");
		return -1;
	}

	return 0;
}

static void
worker_collect_from_port(
	struct dataplane_worker *worker, struct packet_list *sent
) {
	for (uint32_t conn_idx = 0; conn_idx < worker->dataplane->device_count;
	     ++conn_idx) {
		struct worker_tx_connection *tx_conn =
			worker->write_ctx.tx_connections + conn_idx;
		for (uint32_t pipe_idx = 0; pipe_idx < tx_conn->count;
		     ++pipe_idx) {
			data_pipe_item_free(
				tx_conn->pipes + pipe_idx,
				worker_connection_free_cb,
				sent
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

		if (packet->tx_device_id == worker->device_id) {
			mbufs[to_write] = packet_to_mbuf(packet);
			++to_write;
		} else {
			if (worker_send_to_port(ctx, packet)) {
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
	worker_collect_from_port(worker, &sent);
	while ((packet = packet_list_pop(&sent)) != NULL) {
		if (packet->tx_result) {
			packet_list_add(&failed, packet);
			continue;
		}

		// FIXME: per-pipe pending queue
		packet_list_add(&worker->pending, packet);
	}

	while ((packet = packet_list_first(&worker->pending)) != NULL) {
		struct rte_mbuf *mbuf = packet_to_mbuf(packet);
		if (rte_mbuf_refcnt_read(mbuf) != 1)
			break;

		(void)packet_list_pop(&worker->pending);
		rte_pktmbuf_free(mbuf);
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
	struct packet_list input_packets;
	packet_list_init(&input_packets);

	struct packet_list output_packets;
	packet_list_init(&output_packets);

	struct packet_list drop_packets;
	packet_list_init(&drop_packets);

	worker_read(worker, &input_packets);

	struct dp_config *dp_config = worker->instance->dp_config;
	struct cp_config *cp_config = worker->instance->cp_config;
	struct cp_config_gen *cp_config_gen =
		ADDR_OF(&cp_config->cp_config_gen);
	struct config_ectx *config_ectx = ADDR_OF(&cp_config_gen->config_ectx);

	worker->dp_worker->gen = cp_config_gen->gen;
	*worker->dp_worker->iterations += 1;

	for (struct packet *packet = packet_list_first(&input_packets);
	     packet != NULL;
	     packet = packet->next) {
		packet->pipeline_ectx = NULL;

		if (config_ectx == NULL) {
			continue;
		}
		struct phy_device_map *phy_device_map =
			ADDR_OF(&config_ectx->phy_device_maps) +
			packet->rx_device_id;
		struct device_ectx *device_ectx =
			ADDR_OF(phy_device_map->vlan + packet->vlan);
		if (device_ectx == NULL)
			device_ectx = ADDR_OF(phy_device_map->vlan);

		if (device_ectx == NULL) {
			continue;
		}

		if (device_ectx->pipeline_map_size == 0) {
			LOG(TRACE,
			    "pipeline_map size is 0 for device %d",
			    packet->rx_device_id);
			continue;
		}
		packet->pipeline_ectx =
			ADDR_OF(device_ectx->pipeline_map +
				(packet->hash % device_ectx->pipeline_map_size)
			);
	}

	// Now group packets by pipeline and build packet_front
	while (packet_list_first(&input_packets)) {
		struct pipeline_ectx *pipeline_ectx =
			packet_list_first(&input_packets)->pipeline_ectx;

		struct packet_front packet_front;
		packet_front_init(&packet_front);

		// List of packets with different pipeline assigned
		struct packet_list ready_packets;
		packet_list_init(&ready_packets);

		struct packet *packet;
		while ((packet = packet_list_pop(&input_packets))) {
			if (packet->pipeline_ectx == pipeline_ectx) {
				packet_front_output(&packet_front, packet);
			} else {
				packet_list_add(&ready_packets, packet);
			}
		}

		if (pipeline_ectx == NULL) {
			packet_list_concat(&drop_packets, &packet_front.output);
			packet_list_init(&packet_front.output);
		} else {
			// Process pipeline and push packets into drop and write
			// lists
			pipeline_ectx_process(
				dp_config,
				worker->dp_worker,
				cp_config_gen,
				pipeline_ectx,
				&packet_front
			);
		}

		packet_list_concat(&drop_packets, &packet_front.drop);
		packet_list_concat(&output_packets, &packet_front.output);
		packet_list_concat(&output_packets, &packet_front.bypass);

		packet_list_concat(&input_packets, &ready_packets);
	}

	worker_write(worker, &output_packets);

	/*
	 * `output_packets` now contains failed-to-transmit packets which
	 * should be freed.
	 */
	packet_list_concat(&drop_packets, &output_packets);
	dataplane_drop_packets(worker->dataplane, &drop_packets);
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

	worker->read_ctx.read_size = 32;
	worker->write_ctx.write_size = 32;
	worker->write_ctx.rx_pipes = NULL;

	packet_list_init(&worker->pending);

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

	worker->rx_mempool = rte_mempool_create(
		mempool_name,
		16384,
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

	counter_registry_register(&dp_config->worker_counters, "iterations", 1);

	counter_registry_register(&dp_config->worker_counters, "rx", 2);
	counter_registry_register(&dp_config->worker_counters, "tx", 2);
	counter_registry_register(&dp_config->worker_counters, "remote_rx", 2);

	counter_registry_register(&dp_config->worker_counters, "remote_tx", 2);

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
