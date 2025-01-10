#include "dpdk.h"

#include <stdio.h>

#include <rte_dev.h>
#include <rte_eal.h>
#include <rte_ether.h>

int
dpdk_init(
	const char *binary, size_t port_count, const char *const *port_names
) {
	char buffer[1024];
	int bufferPosition = 0;

	unsigned int eal_argc = 0;
	char *eal_argv[128];
#define insert_eal_arg(args...)                                                \
	do {                                                                   \
		eal_argv[eal_argc++] = &buffer[bufferPosition];                \
		bufferPosition += snprintf(                                    \
			&buffer[bufferPosition],                               \
			sizeof(buffer) - bufferPosition,                       \
			##args                                                 \
		);                                                             \
		bufferPosition++;                                              \
	} while (0)

	insert_eal_arg("%s", binary);

	// FIXME use huge pages if configured
	//	insert_eal_arg("--proc-type=primary");

	for (size_t port_idx = 0; port_idx < port_count; ++port_idx) {
		insert_eal_arg("-a");
		insert_eal_arg("%s", port_names[port_idx]);
	}

	insert_eal_arg("--no-huge");
	insert_eal_arg("-m");
	insert_eal_arg("16384");

	insert_eal_arg("-c");
	insert_eal_arg("0x3e2");

	eal_argv[eal_argc] = NULL;

	return rte_eal_init(eal_argc, eal_argv);
}

int
dpdk_add_vdev_port(
	const char *port_name,
	const char *name,
	const struct rte_ether_addr *ether_addr,
	uint16_t queue_count,
	uint16_t numa_id
) {
	(void)numa_id;

	char mac_addr[32];
	snprintf(
		mac_addr,
		sizeof(mac_addr),
		"%02X:%02X:%02X:%02X:%02X:%02X",
		ether_addr->addr_bytes[0],
		ether_addr->addr_bytes[1],
		ether_addr->addr_bytes[2],
		ether_addr->addr_bytes[3],
		ether_addr->addr_bytes[4],
		ether_addr->addr_bytes[5]
	);

	char vdev_args[256];
	snprintf(
		vdev_args,
		sizeof(vdev_args),
		"path=/dev/vhost-net,queues=%d,queue_size=%d,iface=%s,mac=%s",
		queue_count,
		4096,
		name,
		mac_addr
	);

	return rte_eal_hotplug_add("vdev", port_name, vdev_args);
}
