#pragma once

#include <stddef.h>
#include <stdint.h>

struct rte_ether_addr;

int
dpdk_init(
	const char *binary,
	uint64_t dpdk_memory,
	const char *iova_mode,
	size_t port_count,
	const char *const *port_names
);

int
dpdk_add_vdev_port(
	const char *port_name,
	const char *name,
	const char *mac_addr,
	uint16_t queue_count,
	uint16_t queue_size
);

int
dpdk_add_ring_port(const char *port_name);

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

// Query the port's RSS hash key into caller-owned key_buf (of size
// key_buf_len).
//
// On success writes the actual key length to *key_len. Returns non-zero if
// the driver does not report RSS state (unsupported, disabled, or the
// buffer is too small).
int
dpdk_port_get_rss_key(
	uint16_t port_id,
	uint8_t *key_buf,
	uint16_t key_buf_len,
	uint16_t *key_len
);

// Query the port's redirection table into caller-owned reta_buf (of
// reta_buf_len entries), decoding each populated RETA slot to its target
// rx-queue id.
//
// On success writes the actual table size to *reta_size. Returns non-zero
// if the driver does not report RETA state (unsupported, disabled, or the
// buffer is too small).
int
dpdk_port_get_rss_reta(
	uint16_t port_id,
	uint16_t *reta_buf,
	uint16_t reta_buf_len,
	uint16_t *reta_size
);
