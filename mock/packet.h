#pragma once

#include "../../lib/dataplane/packet/packet.h"

////////////////////////////////////////////////////////////////////////////////

struct packet_handle_result {
	struct packet_list output_packets;
	struct packet_list drop_packets;
};