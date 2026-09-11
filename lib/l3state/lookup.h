#pragma once

#include <stdint.h>

#include "l3state.h"
#include "lib/statemap/fwtable.h"

struct packet;

/*
 * Build the l3 state key of a packet: its source address and source port.
 *
 * The family byte plus the zero-padded address keep IPv4 and IPv6 flows
 * apart within one table; both port fields are stored little-endian. The
 * packet must carry a TCP or UDP transport header.
 */
void
l3s_key_of_packet(struct packet *packet, struct l3s_key *key);
