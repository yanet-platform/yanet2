#pragma once

#include "common/network.h"

// Forward declaration to avoid circular dependency
typedef struct fwmap fwmap_t;

/**
 * FWState configuration structure
 * Contains fwmap references and sync configuration
 * Maps are owned by fwstate module, referenced by ACL module
 */

struct fwstate_timeouts {
	uint64_t tcp_syn_ack; // default
	uint64_t tcp_syn;     // default
	uint64_t tcp_fin;     // default
	uint64_t tcp;	      // default (120)
	uint64_t udp;	      // 30
	uint64_t default_;    // 16
};

struct fwstate_sync_config {
	uint8_t src_addr[16];

	struct ether_addr dst_ether;
	uint8_t dst_addr_multicast[16];
	uint16_t port_multicast;
	uint8_t dst_addr_unicast[16];
	uint16_t port_unicast;

	struct fwstate_timeouts timeouts;
};

struct fwstate_config {
	fwmap_t *fw4state; // IPv4 state map
	fwmap_t *fw6state; // IPv6 state map
	struct fwstate_sync_config sync_config;
};
