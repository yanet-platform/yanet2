#pragma once

#include <stdint.h>

struct cp_module;
struct packet_front;

void
process_packets(struct cp_module *cp_module, struct packet_front *packet_front);