#include "topology.h"

#include "common/memory.h"
#include "common/memory_address.h"

#include "lib/dataplane/config/zone.h"

struct dp_port *
dp_topology_alloc_devices(struct dp_config *dp_config, size_t count) {
	struct dp_port *ports = (struct dp_port *)memory_balloc(
		&dp_config->memory_context, sizeof(struct dp_port) * count
	);
	if (ports == NULL) {
		return NULL;
	}

	dp_config->dp_topology.device_count = count;
	SET_OFFSET_OF(&dp_config->dp_topology.devices, ports);

	return ports;
}
