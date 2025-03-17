#include "dataplane.h"

#include "config.h"

#include <stdint.h>

#include <dlfcn.h>
#include <pthread.h>

#include <rte_ethdev.h>
#include <rte_ether.h>

#include "dpdk.h"

#include "common/data_pipe.h"
#include "common/exp_array.h"

#include "common/hugepages.h"
#include "logging/log.h"

#include "dataplane/config/zone.h"

#include "dataplane/device.h"
#include "dataplane/worker.h"

#include <unistd.h>

#include "linux/mman.h"
#include "sys/mman.h"
#include <fcntl.h>

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
dataplane_connect_devices(
	struct dataplane *dataplane,
	uint64_t connection_count,
	struct dataplane_connection_config *connections
)

{
	for (uint64_t conn_idx = 0; conn_idx < connection_count; ++conn_idx) {
		struct dataplane_connection_config *connection =
			connections + conn_idx;
		// FIXME device id should be ferivied
		dataplane_connect_device(
			dataplane,
			dataplane->devices + connection->src_device_id,
			dataplane->devices + connection->dst_device_id
		);
	}

	return 0;
}

static int
dataplane_create_devices(
	struct dataplane *dataplane,
	uint64_t device_count,
	struct dataplane_device_config *device_configs
) {

	dataplane->device_count = device_count;
	dataplane->devices = (struct dataplane_device *)malloc(
		sizeof(struct dataplane_device) * dataplane->device_count
	);

	for (uint64_t dev_idx = 0; dev_idx < device_count; ++dev_idx) {
		struct dataplane_device_config *device_config =
			device_configs + dev_idx;
		if (!strncmp(
			    device_config->port_name,
			    "virtio_user_",
			    strlen("virtio_user_")
		    )) {
			// FIXME handle error
			(void)dpdk_add_vdev_port(
				device_config->port_name,
				device_config->port_name +
					strlen("virtio_user_"),
				device_config->mac_addr,
				device_config->worker_count
			);
		}

		// FIXME: handle port initializations bellow
		(void)dataplane_device_init(
			dataplane,
			dataplane->devices + dev_idx,
			dev_idx,
			device_config
		);
	}

	return 0;
}

int
dataplane_load_module(
	struct dp_config *dp_config, void *bin_hndl, const char *name
) {
	LOG(INFO, "load module %s", name);
	char loader_name[64];
	snprintf(loader_name, sizeof(loader_name), "%s%s", "new_module_", name);
	module_load_handler loader =
		(module_load_handler)dlsym(bin_hndl, loader_name);
	struct module *module = loader();

	struct dp_module *dp_modules = ADDR_OF(&dp_config->dp_modules);
	if (mem_array_expand_exp(
		    &dp_config->memory_context,
		    (void **)&dp_modules,
		    sizeof(*dp_modules),
		    &dp_config->module_count
	    )) {
		LOG(ERROR, "failed to allocate memory for module %s", name);
		// FIXME: free module
		return -1;
	}

	struct dp_module *dp_module = dp_modules + dp_config->module_count - 1;

	strncpy(dp_module->name, module->name, 80);
	dp_module->handler = module->handler;

	SET_OFFSET_OF(&dp_config->dp_modules, dp_modules);

	return 0;
}

int
dataplane_init_storage(
	uint32_t numa_map,
	uint32_t numa_idx,
	void *storage,
	size_t dp_memory,
	size_t cp_memory,

	uint64_t device_count,

	struct dp_config **res_dp_config,
	struct cp_config **res_cp_config
) {
	struct dp_config *dp_config = (struct dp_config *)storage;

	dp_config->numa_map = numa_map;
	dp_config->numa_idx = numa_idx;
	dp_config->storage_size = dp_memory + cp_memory;

	block_allocator_init(&dp_config->block_allocator);
	block_allocator_put_arena(
		&dp_config->block_allocator,
		storage + sizeof(struct dp_config),
		dp_memory - sizeof(struct dp_config)
	);
	memory_context_init(
		&dp_config->memory_context, "dp", &dp_config->block_allocator
	);

	dp_config->config_lock = 0;

	dp_config->dp_modules = NULL;
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
	struct cp_agent_registry *cp_agent_registry =
		(struct cp_agent_registry *)memory_balloc(
			&cp_config->memory_context,
			sizeof(struct cp_agent_registry)
		);
	cp_agent_registry->count = 0;
	SET_OFFSET_OF(&cp_config->agent_registry, cp_agent_registry);

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

	struct cp_device_registry *device_registry =
		(struct cp_device_registry *)memory_balloc(
			&cp_config->memory_context,
			sizeof(struct cp_device_registry) +
				sizeof(uint64_t) * device_count
		);
	device_registry->count = device_count;
	for (uint64_t dev_idx = 0; dev_idx < device_count; ++dev_idx) {
		// FIXME invalid pipeline id
		device_registry->pipelines[dev_idx] = -1;
	}

	struct cp_config_gen *cp_config_gen =
		(struct cp_config_gen *)memory_balloc(
			&cp_config->memory_context, sizeof(struct cp_config_gen)
		);
	SET_OFFSET_OF(&cp_config_gen->module_registry, cp_module_registry);
	SET_OFFSET_OF(&cp_config_gen->pipeline_registry, cp_pipeline_registry);
	SET_OFFSET_OF(&cp_config_gen->device_registry, device_registry);
	SET_OFFSET_OF(&cp_config->cp_config_gen, cp_config_gen);

	SET_OFFSET_OF(&dp_config->cp_config, cp_config);

	*res_dp_config = dp_config;
	*res_cp_config = cp_config;

	return 0;
}

int
dataplane_init(
	struct dataplane *dataplane,
	const char *binary,
	struct dataplane_config *config
) {
	void *bin_hndl = dlopen(NULL, RTLD_NOW | RTLD_GLOBAL);

	dataplane->node_count = config->numa_count;
	LOG(INFO, "initialize dataplane with %u numa", config->numa_count);

	off_t numa_size = config->dp_memory + config->cp_memory;
	off_t storage_size = numa_size * config->numa_count;
	// FIXME: handle errors
	int mem_fd = open(
		config->storage, O_CREAT | O_TRUNC | O_RDWR, S_IRUSR | S_IWUSR
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

	if (storage == MAP_FAILED) {
		int err = errno;
		LOG(ERROR,
		    "failed to create memory-mapped storage %s: "
		    "%s",
		    config->storage,
		    strerror(errno));

		if (err == ENOMEM && is_file_on_hugepages_fs(mem_fd) == 1) {
			LOG(ERROR,
			    "the storage %s is meant to be allocated on "
			    "HUGETLBFS, but there is no memory. Maybe because "
			    "either there are no preallocated pages or another "
			    "process have consumed the memory",
			    config->storage);
		}

		return -1;
	}

	uint32_t numa_map = 0;
	for (uint32_t node_idx = 0; node_idx < dataplane->node_count;
	     ++node_idx) {
		numa_map |= 1 << node_idx;
	}

	for (uint32_t node_idx = 0; node_idx < dataplane->node_count;
	     ++node_idx) {
		struct dataplane_numa_node *node = dataplane->nodes + node_idx;

		LOG(INFO, "initialize storage for NUMA %u", node_idx);
		int rc = dataplane_init_storage(
			numa_map,
			node_idx,
			storage + numa_size * node_idx,
			config->dp_memory,
			config->cp_memory,
			config->device_count,
			&node->dp_config,
			&node->cp_config
		);
		if (rc == -1) {
			LOG(ERROR,
			    "failed to initialize storage for NUMA %u",
			    node_idx);
			return -1;
		}

		node->dp_config->dp_topology.device_count =
			config->device_count;

		// FIXME: load modules into dp memory
		dataplane_load_module(node->dp_config, bin_hndl, "forward");
		dataplane_load_module(node->dp_config, bin_hndl, "route");
		dataplane_load_module(node->dp_config, bin_hndl, "balancer");
	}

	size_t pci_port_count = 0;
	const char **pci_port_names =
		(const char **)malloc(sizeof(char *) * config->device_count);
	if (pci_port_names == NULL) {
		LOG(ERROR, "failed to allocate 'pci_port_names'");
		return -1;
	}
	for (uint64_t dev_idx = 0; dev_idx < config->device_count; ++dev_idx) {
		struct dataplane_device_config *device =
			config->devices + dev_idx;
		if (strncmp(device->port_name,
			    "virtio_user_",
			    strlen("virtio_user_"))) {
			pci_port_names[pci_port_count++] = device->port_name;
		}
	}

	LOG(INFO, "initialize dpdk");
	(void
	)dpdk_init(binary, config->dpdk_memory, pci_port_count, pci_port_names);

	LOG(INFO, "create devices");
	dataplane_create_devices(
		dataplane, config->device_count, config->devices
	);

	LOG(INFO, "connect devices");
	dataplane_connect_devices(
		dataplane, config->connection_count, config->connections
	);

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
	struct dp_config *dp_config,
	struct cp_config *cp_config,
	struct packet_list *packets
) {
	(void)dp_config;

	struct cp_config_gen *config_gen = ADDR_OF(&cp_config->cp_config_gen);
	struct cp_device_registry *device_registry =
		ADDR_OF(&config_gen->device_registry);

	for (struct packet *packet = packet_list_first(packets); packet != NULL;
	     packet = packet->next) {
		if (packet->rx_device_id >= device_registry->count) {
			// FIXME invalid pipeline id
			packet->pipeline_idx = -1;
		} else {
			packet->pipeline_idx =
				device_registry
					->pipelines[packet->rx_device_id];
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
