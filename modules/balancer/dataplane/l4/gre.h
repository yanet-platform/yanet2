#pragma once

#include <stdbool.h>

struct packet;

/*
 * Insert a GRE header between the outer IP header and the inner payload.
 *
 * Shifts L2+outer-L3 forward by sizeof(rte_gre_hdr) bytes, places a
 * zeroed GRE header in the gap, and adjusts the outer IP header's
 * protocol/length fields. IPv4 checksum is recomputed.
 *
 * is_outer_ipv6  — whether the outer tunnel header is IPv6.
 * is_inner_ipv4  — whether the inner (encapsulated) packet is IPv4.
 */
void
insert_gre_header(
	struct packet *packet, bool is_outer_ipv6, bool is_inner_ipv4
);
