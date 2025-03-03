#include "dataplane.h"

#include <stdint.h>

#include <dlfcn.h>
#include <pthread.h>

#include <rte_ethdev.h>
#include <rte_ether.h>

#include "dpdk.h"

#include "drivers/sock_dev.h"

#include "common/data_pipe.h"

#include "dataplane/config/zone.h"

// FIXME: move this to control plane
#include "modules/balancer/config.h"
#include "modules/forward/config.h"
#include "modules/route/config.h"

#include <unistd.h>

#include "linux/mman.h"
#include "sys/mman.h"
#include <fcntl.h>

/*
 * static int
dataplane_sock_port_init(
	struct dataplane *dataplane,
	struct dataplane_device *device,
	const char *sock_name,
	const char *name,
	uint16_t queue_count,
	uint16_t numa_id)
{
	device->queue_count = 0;

	struct rte_eth_conf port_conf;
	memset(&port_conf, 0, sizeof(struct rte_eth_conf));
	// FIXME: do not use constant here
	port_conf.rxmode.max_lro_pkt_size = 8000;

	// FIXME handle errors
	device->port_id = sock_dev_create(sock_name, name, numa_id);
	if (rte_eth_dev_configure(
		device->port_id,
		queue_count,
		queue_count,
		&port_conf)) {
		return -1;
	}

	// FIXME handle errors
	device->workers = (struct dataplane_worker *)
		malloc(sizeof(struct dataplane_worker) * queue_count);

	for (device->queue_count = 0;
	     device->queue_count < queue_count;
	     ++device->queue_count) {
		// FIXME: handle errors
		dataplane_worker_init(
			dataplane,
			device,
			device->workers + device->queue_count,
			device->queue_count);
	}

	// FIXME handle errors
	rte_eth_dev_start(device->port_id);

	return 0;
}
*/

static int
dataplane_worker_connect(
	struct dataplane *dataplane,
	struct dataplane_device *device,
	struct dataplane_worker *wrk_tx,
	struct worker_tx_connection *tx_conn,
	struct dataplane_worker *wrk_rx
) {
	(void)dataplane;
	(void)device;
	(void)wrk_tx;

	if (!(tx_conn->count & (tx_conn->count + 1))) {
		struct data_pipe *pipes = (struct data_pipe *)realloc(
			tx_conn->pipes,
			sizeof(struct data_pipe) * 2 * (tx_conn->count + 1)
		);
		if (pipes == NULL)
			return -1;
		tx_conn->pipes = pipes;
	}

	if (!(wrk_rx->write_ctx.rx_pipe_count &
	      (wrk_rx->write_ctx.rx_pipe_count + 1))) {
		struct data_pipe *pipes = (struct data_pipe *)realloc(
			wrk_rx->write_ctx.rx_pipes,
			sizeof(struct data_pipe) * 2 *
				(wrk_rx->write_ctx.rx_pipe_count + 1)
		);
		if (pipes == NULL)
			return -1;
		wrk_rx->write_ctx.rx_pipes = pipes;
	}

	struct data_pipe *pipe = tx_conn->pipes + tx_conn->count;
	if (data_pipe_init(pipe, 10))
		return -1;

	++tx_conn->count;

	*(wrk_rx->write_ctx.rx_pipes + wrk_rx->write_ctx.rx_pipe_count++) =
		*pipe;

	return 0;
}

static int
dataplane_connect_device(
	struct dataplane *dataplane,
	struct dataplane_device *from_device,
	struct dataplane_device *to_device
) {
	/*
	 * Each worker from source device should have at least one
	 * connection to destination device. Also create at least one
	 * incoming connection from source device for each destination
	 * device worker.
	 */
	size_t pipe_count = from_device->worker_count;
	if (to_device->worker_count > pipe_count)
		pipe_count = to_device->worker_count;

	for (size_t pipe_idx = 0; pipe_idx < pipe_count; ++pipe_idx) {
		// Select source and destination workers
		struct dataplane_worker *from_worker =
			from_device->workers +
			pipe_idx % from_device->worker_count;

		struct dataplane_worker *to_worker =
			to_device->workers + pipe_idx % to_device->worker_count;

		struct worker_tx_connection *tx_conn =
			from_worker->write_ctx.tx_connections +
			to_device->device_id;

		// FIXME: handle errors
		dataplane_worker_connect(
			dataplane, from_device, from_worker, tx_conn, to_worker
		);
	}

	return 0;
}

/*
 * This function creates device interconnect topology which heavily depends
 * on default virtual devices creation policy.
 */
static int
dataplane_connect_devices(struct dataplane *dataplane)

{
	/*
	 * FIXME: the code bellow is about device interconnect topology so
	 * there is full-mesh connection between dpdk `physical` devices and
	 * each `physical` device is connected with its `virtual` counterpart.
	 * In fact `data_pipe` is one directional connection (which allows
	 * processing feedback (as producer should know if data are consumed)
	 * so we create two connections for each device.
	 * However, it is legal to create one directional flow between
	 * devices (e.g. span port).
	 */

	size_t phy_device_count = dataplane->device_count / 2;

	// Create physical devices full-mesh interconnection
	for (size_t dev1_idx = 0; dev1_idx < phy_device_count; ++dev1_idx) {
		for (size_t dev2_idx = dev1_idx + 1;
		     dev2_idx < phy_device_count;
		     ++dev2_idx) {
			dataplane_connect_device(
				dataplane,
				dataplane->devices + dev1_idx,
				dataplane->devices + dev2_idx
			);

			dataplane_connect_device(
				dataplane,
				dataplane->devices + dev2_idx,
				dataplane->devices + dev1_idx
			);
		}
	}

	// Create interconnect between physical and virtual device pair
	for (size_t dev1_idx = 0; dev1_idx < phy_device_count; ++dev1_idx) {
		dataplane_connect_device(
			dataplane,
			dataplane->devices + dev1_idx,
			dataplane->devices + dev1_idx + phy_device_count
		);

		dataplane_connect_device(
			dataplane,
			dataplane->devices + dev1_idx + phy_device_count,
			dataplane->devices + dev1_idx
		);
	}

	return 0;
}

static int
dataplane_create_devices(
	struct dataplane *dataplane,
	size_t device_count,
	const char *const *devices
) {

	dataplane->device_count = device_count * 2;
	dataplane->devices = (struct dataplane_device *)malloc(
		sizeof(struct dataplane_device) * dataplane->device_count
	);

	struct dataplane_device_config phy_config;

	for (size_t dev_idx = 0; dev_idx < device_count; ++dev_idx) {
		phy_config.device_id = dev_idx;
		phy_config.rss_hash = RTE_ETH_RSS_IP;
		phy_config.mtu = 8000;
		phy_config.max_lro_packet_size = 8200;
		phy_config.worker_count = 4;
		phy_config.workers = (struct dataplane_device_worker_config[]){
			{
				.core_id = 26,
				.numa_id = 0,
			},
			{
				.core_id = 27,
				.numa_id = 0,
			},
			{
				.core_id = 28,
				.numa_id = 0,
			},
			{
				.core_id = 29,
				.numa_id = 0,
			},
		};
		// FIXME: handle port initializations bellow
		(void)dataplane_dpdk_port_init(
			dataplane,
			dataplane->devices + dev_idx,
			devices[dev_idx],
			&phy_config
		);
	}

	struct dataplane_device_config virt_config;
	for (size_t dev_idx = 0; dev_idx < device_count; ++dev_idx) {
		char vdev_name[64];
		snprintf(
			vdev_name,
			sizeof(vdev_name),
			"virtio_user_kni%lu",
			dev_idx
		);

		struct rte_ether_addr ether_addr;
		// FIXME: handle port initializations bellow
		(void)dataplane_dpdk_port_get_mac(
			dataplane->devices + dev_idx, &ether_addr
		);

		(void)dpdk_add_vdev_port(
			vdev_name,
			vdev_name + strlen("virtio_user_"),
			&ether_addr,
			1,
			0
		);

		virt_config.device_id = device_count + dev_idx;
		virt_config.rss_hash = 0;
		virt_config.mtu = 8000;
		virt_config.max_lro_packet_size = 8200;
		virt_config.worker_count = 1;
		virt_config.workers = (struct dataplane_device_worker_config[]){
			{
				.core_id = 30,
				.numa_id = 0,
			},
		};

		(void)dataplane_dpdk_port_init(
			dataplane,
			dataplane->devices + device_count + dev_idx,
			vdev_name,
			&virt_config
		);
	}

	return 0;
}

int
dataplane_load_module(
	struct dp_config *dp_config, void *bin_hndl, const char *name
) {
	char loader_name[64];
	snprintf(loader_name, sizeof(loader_name), "%s%s", "new_module_", name);
	module_load_handler loader =
		(module_load_handler)dlsym(bin_hndl, loader_name);
	struct module *module = loader();

	struct dp_module *dp_modules =
		ADDR_OF(dp_config, dp_config->dp_modules);
	if (mem_array_expand_exp(
		    &dp_config->memory_context,
		    (void **)&dp_modules,
		    sizeof(*dp_modules),
		    &dp_config->module_count
	    )) {
		// FIXME: free module
		return -1;
	}

	struct dp_module *dp_module = dp_modules + dp_config->module_count - 1;

	strncpy(dp_module->name, module->name, 80);
	dp_module->handler = module->handler;

	dp_config->dp_modules = OFFSET_OF(dp_config, dp_modules);

	return 0;
}

int
dataplane_init_storage(
	const char *storage_name,

	size_t dp_memory,
	size_t cp_memory,

	struct dp_config **res_dp_config,
	struct cp_config **res_cp_config
) {
	off_t storage_size = dp_memory + cp_memory;

	// FIXME: handle errors
	int mem_fd =
		open(storage_name, O_CREAT | O_TRUNC | O_RDWR, S_IRUSR | S_IWUSR
		);
	if (mem_fd < 0)
		return -1;

	if (ftruncate(mem_fd, storage_size)) {
		close(mem_fd);
		return -1;
	}

	void *storage =
		mmap(NULL,
		     storage_size,
		     PROT_READ | PROT_WRITE,
		     MAP_SHARED,
		     mem_fd,
		     0);
	close(mem_fd);

	if ((intptr_t)storage == -1) {
		return -1;
	}

	struct dp_config *dp_config = (struct dp_config *)storage;

	block_allocator_init(&dp_config->block_allocator);
	block_allocator_put_arena(
		&dp_config->block_allocator,
		storage + sizeof(struct dp_config),
		dp_memory - sizeof(struct dp_config)
	);
	memory_context_init(
		&dp_config->memory_context, "dp", &dp_config->block_allocator
	);

	dp_config->dp_modules = OFFSET_OF(dp_config, (struct dp_module *)NULL);
	dp_config->module_count = 0;

	struct cp_config *cp_config =
		(struct cp_config *)((uintptr_t)storage + dp_memory);

	block_allocator_init(&cp_config->block_allocator);
	block_allocator_put_arena(
		&cp_config->block_allocator,
		storage + dp_memory + sizeof(struct cp_config),
		cp_memory - sizeof(struct cp_config)
	);
	memory_context_init(
		&cp_config->memory_context, "cp", &cp_config->block_allocator
	);

	// FIXME: cp_config bootstrap routine
	struct cp_module_registry *cp_module_registry =
		(struct cp_module_registry *)memory_balloc(
			&cp_config->memory_context,
			sizeof(struct cp_module_registry)
		);
	cp_module_registry->count = 0;

	struct cp_pipeline_registry *cp_pipeline_registry =
		(struct cp_pipeline_registry *)memory_balloc(
			&cp_config->memory_context,
			sizeof(struct cp_pipeline_registry)
		);
	cp_pipeline_registry->count = 0;

	struct cp_config_gen *cp_config_gen =
		(struct cp_config_gen *)memory_balloc(
			&cp_config->memory_context, sizeof(struct cp_config_gen)
		);
	cp_config_gen->module_registry =
		OFFSET_OF(cp_config_gen, cp_module_registry);
	cp_config_gen->pipeline_registry =
		OFFSET_OF(cp_config_gen, cp_pipeline_registry);
	cp_config->cp_config_gen = OFFSET_OF(cp_config, cp_config_gen);

	dp_config->cp_config = OFFSET_OF(dp_config, cp_config);

	*res_dp_config = dp_config;
	*res_cp_config = cp_config;

	return 0;
}

int
dataplane_init(
	struct dataplane *dataplane,
	const char *binary,

	const char *storage,

	size_t numa_count,
	size_t dp_memory,
	size_t cp_memory,

	size_t device_count,
	const char *const *devices
) {
	void *bin_hndl = dlopen(NULL, RTLD_NOW | RTLD_GLOBAL);

	dataplane->node_count = numa_count;
	for (uint32_t node_idx = 0; node_idx < dataplane->node_count;
	     ++node_idx) {
		struct dataplane_numa_node *node = dataplane->nodes + node_idx;

		char storage_name[64];
		snprintf(
			storage_name,
			sizeof(storage_name),
			"%s-%u",
			storage,
			node_idx
		);

		int rc = dataplane_init_storage(
			storage_name,
			dp_memory,
			cp_memory,
			&node->dp_config,
			&node->cp_config
		);
		if (rc == -1) {
			return -1;
		}

		uint16_t *forward_map = (uint16_t *)memory_balloc(
			&node->dp_config->memory_context,
			sizeof(uint16_t) * device_count * 2
		);

		for (uint16_t dev_idx = 0; dev_idx < device_count; ++dev_idx) {
			forward_map[dev_idx] = dev_idx + device_count;
			forward_map[device_count + dev_idx] = dev_idx;
		}

		node->dp_config->dp_topology.device_count = device_count * 2;
		node->dp_config->dp_topology.forward_map =
			OFFSET_OF(&node->dp_config->dp_topology, forward_map);

		// FIXME: load modules into dp memory
		dataplane_load_module(node->dp_config, bin_hndl, "forward");
		dataplane_load_module(node->dp_config, bin_hndl, "route");
		dataplane_load_module(node->dp_config, bin_hndl, "balancer");
	}

	(void)dpdk_init(binary, device_count, devices);

	dataplane_create_devices(dataplane, device_count, devices);

	dataplane_connect_devices(dataplane);

	return 0;
}

static void *
stat_thread(void *arg) {
	struct dataplane *dataplane = (struct dataplane *)arg;

	FILE *log = fopen("stat.log", "w");

	(void)dataplane;

	uint64_t read = dataplane->read;
	uint64_t write = dataplane->write;
	uint64_t drop = dataplane->drop;

	struct rte_eth_xstat_name names[4096];
	struct rte_eth_xstat xstats0[dataplane->device_count][4096];

	struct rte_eth_stats stats0[dataplane->device_count];
	for (uint16_t idx = 0; idx < dataplane->device_count; ++idx) {
		rte_eth_stats_get(
			dataplane->devices[idx].port_id, &stats0[idx]
		);
		rte_eth_xstats_get(
			dataplane->devices[idx].port_id, xstats0[idx], 4096
		);
	}

	while (1) {
		sleep(1);

		uint64_t nr = dataplane->read;
		uint64_t nw = dataplane->write;
		uint64_t nd = dataplane->drop;

		fprintf(log,
			"dp %lu %lu %lu\n",
			nr - read,
			nw - write,
			nd - drop);
		read = nr;
		write = nw;
		drop = nd;

		for (uint16_t idx = 0; idx < dataplane->device_count; ++idx) {
			struct rte_eth_stats stats1;
			rte_eth_stats_get(
				dataplane->devices[idx].port_id, &stats1
			);
			fprintf(log,
				"dev %u ib %li ob %li ip %li op %li ie %li oe "
				"%li\n",
				idx,
				(int64_t)(stats1.ibytes - stats0[idx].ibytes),
				(int64_t)(stats1.obytes - stats0[idx].obytes),
				(int64_t)(stats1.ipackets - stats0[idx].ipackets
				),
				(int64_t)(stats1.opackets - stats0[idx].opackets
				),
				(int64_t)(stats1.ierrors - stats0[idx].ierrors),
				(int64_t)(stats1.oerrors - stats0[idx].oerrors)
			);

			memcpy(&stats0[idx], &stats1, sizeof(stats1));

			struct rte_eth_xstat xstats1[4096];
			rte_eth_xstats_get_names(
				dataplane->devices[idx].port_id, names, 4096
			);
			int cnt = rte_eth_xstats_get(
				dataplane->devices[idx].port_id, xstats1, 4096
			);

			for (int pth = 0; pth < cnt; ++pth) {
				fprintf(log,
					"xstat %u %s %lu\n",
					idx,
					names[xstats1[pth].id].name,
					xstats1[pth].value -
						xstats0[idx][pth].value);
			}

			memcpy(&xstats0[idx],
			       xstats1,
			       sizeof(struct rte_eth_xstat) * cnt);
		}

		fflush(log);
	}

	return NULL;
}

int
dataplane_start(struct dataplane *dataplane) {
	for (size_t dev_idx = 0; dev_idx < dataplane->device_count; ++dev_idx) {
		dataplane_device_start(dataplane, dataplane->devices + dev_idx);
	}

	pthread_t thread_id;
	pthread_create(&thread_id, NULL, stat_thread, dataplane);

	return 0;
}

int
dataplane_stop(struct dataplane *dataplane) {
	for (size_t dev_idx = 0; dev_idx < dataplane->device_count; ++dev_idx) {
		dataplane_device_stop(dataplane->devices + dev_idx);
	}

	return 0;
}

void
dataplane_route_pipeline(
	struct dataplane *dataplane, struct packet_list *packets
) {
	// FIXME: select proper NUMA node

	for (struct packet *packet = packet_list_first(packets); packet != NULL;
	     packet = packet->next) {
		if (packet->rx_device_id >= dataplane->device_count / 2) {
			packet->pipeline_idx = 1;
		} else {
			packet->pipeline_idx = 0;
		}
	}
}

void
dataplane_drop_packets(
	struct dataplane *dataplane, struct packet_list *packets
) {
	(void)dataplane;
	struct packet *packet = packet_list_first(packets);
	while (packet != NULL) {
		__atomic_add_fetch(&dataplane->drop, 1, __ATOMIC_ACQ_REL);
		// Freeing packet will destroy the `next` field to
		struct packet *drop_packet = packet;
		packet = packet->next;

		struct rte_mbuf *mbuf = packet_to_mbuf(drop_packet);
		rte_pktmbuf_free(mbuf);
	}
}
