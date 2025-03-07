#pragma once

#include <stddef.h>
#include <stdint.h>

struct rte_ether_addr;

int
dpdk_init(
	const char *binary,
	uint64_t dpdk_memory,
	size_t port_count,
	const char *const *port_names
);

int
dpdk_add_vdev_port(
	const char *port_name,
	const char *name,
	const char *mac_addr,
	uint16_t queue_count
);

int
dpdk_port_init(
	const char *name,
	uint16_t *port_id,
	uint64_t rss_hash,
	uint16_t rx_queue_count,
	uint16_t tx_queue_count,
	uint16_t mtu,
	uint16_t max_lro_packet_size
);

int
dpdk_port_start(uint16_t port_id);

int
dpdk_port_stop(uint16_t port_id);

int
dpdk_port_get_mac(uint16_t port_id, struct rte_ether_addr *ether_addr);
