#pragma once

// Checks the systest adds on top of the real headers.
//
// The DPDK mbuf is not mirrored as a whole; the packet view only reads three
// of its fields, so their locations are exported as constants that ctest
// compares with the Rust side. The tunnel stripper is a function ctest can
// only test by linking the dataplane, so its prototype is pinned here.

#include <stddef.h>

#include <rte_mbuf.h>

#include "lib/dataplane/packet/decap.h"

#define YANET_RS_MBUF_BUF_ADDR_OFFSET offsetof(struct rte_mbuf, buf_addr)
#define YANET_RS_MBUF_DATA_OFF_OFFSET offsetof(struct rte_mbuf, data_off)
#define YANET_RS_MBUF_DATA_OFF_SIZE sizeof(((struct rte_mbuf *)0)->data_off)
#define YANET_RS_MBUF_DATA_LEN_OFFSET offsetof(struct rte_mbuf, data_len)
#define YANET_RS_MBUF_DATA_LEN_SIZE sizeof(((struct rte_mbuf *)0)->data_len)

_Static_assert(
	__builtin_types_compatible_p(
		__typeof__(&packet_decap), int (*)(struct packet *)
	),
	"packet_decap prototype differs from the Rust mirror"
);
