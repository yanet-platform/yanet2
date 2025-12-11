#include "dataplane.h"
#include <assert.h>
#include <stdio.h>
#include <string.h>

#include "common/container_of.h"
#include "dataplane/time/clock.h"
#include "lib/controlplane/config/econtext.h"
#include "lib/dataplane/config/zone.h"

#include "config.h"

////////////////////////////////////////////////////////////////////////////////

void
my_module_handle_packets(
	struct dp_worker *dp_worker,
	struct module_ectx *module_ectx,
	struct packet_front *packet_front
) {
	assert(dp_worker->idx == 0);

	struct my_module_config *config = container_of(
		ADDR_OF(&module_ectx->cp_module),
		struct my_module_config,
		cp_module
	);
	config->packet_counter += 1;
	config->last_packet_timestamp =
		tsc_clock_get_time_ns(&dp_worker->clock);

	struct packet *packet;
	while ((packet = packet_list_pop(&packet_front->input)) != NULL) {
		packet_front_output(packet_front, packet);
	}
}

struct module *
new_module_balancer() {
	struct module *my_module = malloc(sizeof(struct module));
	memset(my_module->name, 0, sizeof(my_module->name));
	sprintf(my_module->name, "balancer");
	my_module->handler = my_module_handle_packets;
	return my_module;
}