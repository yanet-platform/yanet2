#include "module/route.h"

static void
route_handle_packet(
	struct route_module *route,
	struct pipeline *pipeline,
	struct packet *packet)
{

	uint32_t action = filter_check(&route->filter, packet);
	if (action == FILTER_BYPASS) {
		pipeline_packet_output(pipeline, packet);
		return;
	}

	struct route *route = route_lookup(route, packet);

	if (route == NULL) {
		pipeline_packet_drop(pipeline, packet);
		return;
	}

	switch (route->type) {
	case ROUTE_DIRECT: {


	}

	case ROUTE_TUNNEL: {

	}

	default:
		pipeline_packet_drop(pipeline, packet);
		return;
	}

	pipeline_packet_output(pipeline, packet);
}


static void
route_handle(struct module *module, struct pipeline *pipeline)
{
	struct route_module *route =
		container_of(module, struct route_module, module);

	struct packet_list input = pipeline_packet_input(pipeline);

	for (struct packet *packet == packet_list_first(&input);
	     packet != NULL;
	     packet = packet->next) {
		route_handle_packet(route, pipeline, packet);
	}
}

