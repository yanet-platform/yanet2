#include "dp_shim.h"

#include "lib/dataplane/pipeline/schedule.h"

void
yanet_sys_dp_redirect(
	struct module_ectx *module_ectx,
	struct packet_front *packet_front,
	struct module_device_target *target,
	int to_input,
	struct packet *packet
) {
	if (to_input) {
		module_ectx_route_input(
			module_ectx,
			packet_front,
			target->abs_input_entry,
			packet
		);
	} else {
		module_ectx_route_output(
			module_ectx,
			packet_front,
			target->abs_output_entry,
			packet
		);
	}
}
