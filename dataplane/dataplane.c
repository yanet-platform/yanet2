#include "dataplane.h"

#include <stdint.h>

#include <dlfcn.h>
#include <pthread.h>

#include <rte_ether.h>

#include "dpdk.h"

#include "drivers/sock_dev.h"

#include "data_pipe.h"

#include "dataplane/config/dataplane_registry.h"

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
	if (data_pipe_init(pipe, 4096))
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
	 * each `physcical` device is connected with its `virtual` counterpart.
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

	for (size_t dev_idx = 0; dev_idx < device_count; ++dev_idx) {
		// FIXME: handle port initializations bellow
		(void)dataplane_dpdk_port_init(
			dataplane,
			dataplane->devices + dev_idx,
			dev_idx,
			devices[dev_idx],
			2,
			0
		);
	}

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

		(void)dataplane_dpdk_port_init(
			dataplane,
			dataplane->devices + device_count + dev_idx,
			device_count + dev_idx,
			vdev_name,
			1,
			0
		);
	}

	return 0;
}

int
dataplane_init(
	struct dataplane *dataplane,
	const char *binary,
	size_t device_count,
	const char *const *devices
) {
	void *bin_hndl = dlopen(NULL, RTLD_NOW | RTLD_GLOBAL);

	dataplane->node_count = 2;
	for (uint32_t node_idx = 0; node_idx < dataplane->node_count;
	     ++node_idx) {
		struct dataplane_numa_node *node = dataplane->nodes + node_idx;

		dataplane_registry_init(&node->dataplane_registry);
		dataplane_registry_load_module(
			&node->dataplane_registry, bin_hndl, "route"
		);
		dataplane_registry_load_module(
			&node->dataplane_registry, bin_hndl, "forward"
		);

		uint16_t kernel_map[device_count * 2];
		for (size_t dev_idx = 0; dev_idx < device_count; ++dev_idx)
			kernel_map[dev_idx] = dev_idx + device_count;
		for (size_t dev_idx = 0; dev_idx < device_count; ++dev_idx)
			kernel_map[device_count + dev_idx] = dev_idx;

		struct dataplane_module_config *dmc =
			(struct dataplane_module_config[]
			){{"route", "route0", NULL, 0},
			  {"forward",
			   "from_kernel",
			   kernel_map,
			   sizeof(kernel_map)},
			  {"forward",
			   "to_kernel",
			   kernel_map,
			   sizeof(kernel_map)}};

		struct dataplane_pipeline_config *dpc =
			(struct dataplane_pipeline_config[]
			){{"phy",
			   (struct dataplane_pipeline_module[]
			   ){{"forward", "to_kernel"}, {"route", "route0"}},
			   2},
			  {"virt",
			   (struct dataplane_pipeline_module[]){
				   {"forward", "from_kernel"},
			   },
			   1}};

		dataplane_registry_update(
			&node->dataplane_registry, dmc, 1, dpc, 1
		);

		node->phy_pipeline = pipeline_registry_lookup(
			&node->dataplane_registry.pipeline_registry, "phy"
		);
		node->kernel_pipeline = pipeline_registry_lookup(
			&node->dataplane_registry.pipeline_registry, "virt"
		);
	}

	(void)dpdk_init(binary, device_count, devices);

	dataplane_create_devices(dataplane, device_count, devices);

	dataplane_connect_devices(dataplane);

	return 0;
}

int
dataplane_start(struct dataplane *dataplane) {
	for (size_t dev_idx = 0; dev_idx < dataplane->device_count; ++dev_idx) {
		dataplane_device_start(dataplane, dataplane->devices + dev_idx);
	}

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
			packet->pipeline = dataplane->nodes[0].kernel_pipeline;
		} else {
			packet->pipeline = dataplane->nodes[0].phy_pipeline;
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
		// Freeing packet will destroy the `next` field to
		struct packet *drop_packet = packet;
		packet = packet->next;

		struct rte_mbuf *mbuf = packet_to_mbuf(drop_packet);
		rte_pktmbuf_free(mbuf);
	}
}
