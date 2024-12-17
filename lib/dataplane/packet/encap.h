#pragma once

#include "packet.h"

int
packet_ip4_encap(struct packet *packet, const uint8_t *dst, const uint8_t *src);

int
packet_ip6_encap(struct packet *packet, const uint8_t *dst, const uint8_t *src);
