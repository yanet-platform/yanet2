#include "modules/decap.h"
#include "pipeline.h"

static void
decap_handle_packet(
	struct decap_module *decap,
	struct pipeline *pipeline,
	struct packet *packet)
{

	uint32_t action = filter_check(&decap->filter, packet);
	if (action == FILTER_BYPASS) {
		pipeline_packet_output(pipeline, packet);
		return;
	}

	if (packet_decap(packet) != 0) {
		pipeline_packet_drop(pipeline, packet);
		return;
	}

	pipeline_packet_output(pipeline, packet);
}


static void
decap_handle(struct module *module, struct pipeline *pipeline)
{
	struct decap_module *decap =
		container_of(module, struct decap_module, module);

	struct packet_list input = pipeline_packet_input(pipeline);

	for (struct packet *packet == packet_list_first(&input);
	     packet != NULL;
	     packet = packet->next) {
		decap_handle_packet(decap, pipeline, packet);
	}
}

