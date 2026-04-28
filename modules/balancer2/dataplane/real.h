#pragma once

#include <stdbool.h>

#include "common/network.h"

enum real_flags {
	real_enabled = 1u << 0,
	real_ip6 = 1u << 1,
};

/*
 * A real (backend) server within a virtual service.
 *
 * Stored in a per-VS contiguous array indexed by config index.
 * See struct balancer_vs for the array layout and indexing scheme.
 */
struct real {
	/* Destination IP address of the real server (IPv4 or IPv6). */
	struct net_addr addr;

	/*
	 * Source network for the outer tunnel header.
	 *
	 * Contains both the base address (src.v4.addr / src.v6.addr)
	 * and the mask (src.v4.mask / src.v6.mask).
	 *
	 * INVARIANT: the address bytes must be pre-masked by the
	 * controlplane, i.e. (addr[i] & mask[i]) == addr[i] for every
	 * byte i. The tunnel code relies on this to embed client source
	 * IP bits into the unmasked positions without an extra AND:
	 *
	 *   outer_src[i] = addr[i] | (client_src[i] & ~mask[i])
	 *
	 * Use v4 when balancer_real_ipv6 is clear, v6 when set.
	 */
	struct net src;

	uint64_t counter_id;

	_Atomic uint8_t flags;
};
