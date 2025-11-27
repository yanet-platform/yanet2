#pragma once

#include "packet.h"
#include <stddef.h>

#include "lib/dataplane/config/zone.h"

////////////////////////////////////////////////////////////////////////////////

/// Mock on the yanet worker
struct yanet_worker_mock {
	struct dp_worker dp_worker;
	struct dp_config *dp_config;
	struct cp_config *cp_config;
};

struct packet_handle_result
yanet_worker_mock_handle_packets(
	struct yanet_worker_mock *worker, struct packet_list *input
);