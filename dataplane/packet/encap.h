#ifndef ENCAP_H
#define ENCAP_H

#include "packet.h"

int
packet_ip_encap(
	struct packet *packet,
	struct ip_addr *dst,
	struct ip_addr *src
);

int
packet_gre_decap(
	struct packet *packet,
	struct ip_addr *dst,
	struct ip_addr *src
);

#endif
