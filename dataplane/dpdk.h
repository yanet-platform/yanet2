#ifndef DATAPLANE_DPDK_H
#define DATAPLANE_DPDK_H

#include <stddef.h>
#include <stdint.h>

struct rte_ether_addr;

int
dpdk_init(const char *binary, size_t port_count, const char *const *port_names);

int
dpdk_add_vdev_port(
	const char *port_name,
	const char *name,
	const struct rte_ether_addr *ether_addr,
	uint16_t queue_count,
	uint16_t numa_id
);

#endif
