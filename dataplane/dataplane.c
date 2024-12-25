#include "dataplane.h"

#include <stdint.h>

#include <pthread.h>

#include <rte_ether.h>

#include "dpdk.h"

#include "drivers/sock_dev.h"

// FIXME remove this include
#include "modules/acl.h"
#include "modules/balancer.h"
#include "modules/kernel.h"
#include "modules/route.h"

#include "common/data_pipe.h"

static int
dataplane_init_kernel_pipeline(struct dataplane *dataplane) {
	pipeline_init(&dataplane->kernel_pipeline);

	struct pipeline_module_config_ref kernel_cfg_refs[1];
	kernel_cfg_refs[0] =
		(struct pipeline_module_config_ref){"kernel", "kernel0"};

	return pipeline_configure(
		&dataplane->kernel_pipeline,
		kernel_cfg_refs,
		1,
		&dataplane->config.module_registry
	);
}

static int
dataplane_init_phy_pipeline(struct dataplane *dataplane) {
	pipeline_init(&dataplane->phy_pipeline);

	struct pipeline_module_config_ref phy_cfg_refs[4];
	phy_cfg_refs[0] =
		(struct pipeline_module_config_ref){"kernel", "kernel0"};
	phy_cfg_refs[1] = (struct pipeline_module_config_ref){"acl", "acl0"};
	phy_cfg_refs[2] =
		(struct pipeline_module_config_ref){"balancer", "balancer0"};
	phy_cfg_refs[3] =
		(struct pipeline_module_config_ref){"route", "route0"};

	return pipeline_configure(
		&dataplane->phy_pipeline,
		phy_cfg_refs + 0,
		4,
		&dataplane->config.module_registry
	);
}

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
	dataplane->config.module_registry = (struct module_registry){NULL, 0};

	dataplane_register_module(dataplane, new_module_acl());
	dataplane_register_module(dataplane, new_module_balancer());
	dataplane_register_module(dataplane, new_module_route());
	dataplane_register_module(dataplane, new_module_kernel());

	/*
	 * FIXME: rollback configurations bellow or make registry
	 * configuration transactional
	 */

	if (module_registry_configure(
		    &dataplane->config.module_registry, "acl", "acl0", NULL, 0
	    )) {
		return -1;
	}

	if (module_registry_configure(
		    &dataplane->config.module_registry,
		    "balancer",
		    "balancer0",
		    NULL,
		    0
	    )) {
		return -1;
	}

	if (module_registry_configure(
		    &dataplane->config.module_registry,
		    "route",
		    "route0",
		    NULL,
		    0
	    )) {
		return -1;
	}

	uint16_t kernel_map[8];
	for (size_t dev_idx = 0; dev_idx < device_count; ++dev_idx)
		kernel_map[dev_idx] = dev_idx + device_count;

	for (size_t dev_idx = 0; dev_idx < device_count; ++dev_idx)
		kernel_map[device_count + dev_idx] = dev_idx;

	if (module_registry_configure(
		    &dataplane->config.module_registry,
		    "kernel",
		    "kernel0",
		    kernel_map,
		    sizeof(kernel_map)
	    )) {
		return -1;
	}

	// FIXME: handle errors
	dataplane_init_kernel_pipeline(dataplane);
	dataplane_init_phy_pipeline(dataplane);

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
	for (struct packet *packet = packet_list_first(packets); packet != NULL;
	     packet = packet->next) {
		if (packet->rx_device_id >= dataplane->device_count / 2) {
			packet->pipeline = &dataplane->kernel_pipeline;
		} else {
			packet->pipeline = &dataplane->phy_pipeline;
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

int
dataplane_register_module(struct dataplane *dataplane, struct module *module) {
	struct module_registry *module_registry =
		&dataplane->config.module_registry;

	for (uint32_t idx = 0; idx < module_registry->module_count; ++idx) {
		struct module_config_registry *module_config_registry =
			module_registry->modules + idx;

		if (module_config_registry->module == module) {
			// TODO: error code
			return -1;
		}

		if (!strncmp(
			    module_config_registry->module->name,
			    module->name,
			    MODULE_NAME_LEN
		    )) {
			// TODO: error code
			return -1;
		}
	}

	// Module is not known by pointer nor name

	// FIXME array extending as routine/library
	if (module_registry->module_count % 8 == 0) {
		struct module_config_registry *new_config_registry =
			(struct module_config_registry *)realloc(
				module_registry->modules,
				sizeof(struct module_config_registry) *
					(module_registry->module_count + 8)
			);
		if (new_config_registry == NULL) {
			// TODO: error code
			return -1;
		}
		module_registry->modules = new_config_registry;
	}

	module_registry->modules[module_registry->module_count++] =
		(struct module_config_registry){module, NULL, 0};
	return 0;
}
