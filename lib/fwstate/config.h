#pragma once

#include "common/network.h"

#include "fwstate/types.h"

struct fwstate_sync_config {
	uint8_t src_addr[16];

	struct ether_addr dst_ether;
	uint8_t dst_addr_multicast[16];
	/**
	 * Multicast destination port in network byte order (big-endian).
	 * Stored in BE so it can be used directly in UDP header fields
	 * and compared with udp_hdr->dst_port without conversion.
	 * The controlplane (converters.go ConvertPbToCSyncConfig) performs
	 * the host-to-network conversion when populating this field.
	 */
	uint16_t port_multicast;
	uint8_t dst_addr_unicast[16];
	/**
	 * Unicast destination port in network byte order (big-endian).
	 * Same convention as port_multicast - converted by the controlplane.
	 */
	uint16_t port_unicast;

	struct fwstate_timeouts timeouts;
};
