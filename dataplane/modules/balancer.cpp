#include "modules/balancer.h"

#include "pipeline.h"

static struct balancer_vs *
balancer_vs_lookup(struct balancer_module *balancer, uint32_t action)
{

}

static struct balancer_rs *
balancer_rs_lookup(
	struct balancer_module *module,
	struct balancer_vs *vs,
	struct packet *packet)
{

}

static int
balancer_route(
	struct balancer_module *module,
	struct balancer_vs *vs,
	struct balancer_rs *rs,
	struct packet *packet)
{
	if (vs->options & OPT_ENCAP) {
		return packet_ip_encap(packet, rs->dst, module->source);
	} else if (vs->options & OPT_GRE) {
		return packet_gre_encap(packet, rs->dst, module->source);
	}
	return -1;
}

static void
balancer_handle_packet(
	struct balancer_module *balancer,
	struct pipeline *pipeline,
	struct packet *packet)
{

	uint32_t action = filter_check(&balancer->filter, packet);
	if (action == FILTER_BYPASS) {
		pipeline_packet_output(pipeline, packet);
		return;
	}

	struct balancer_vs *vs = balancer_vs_lookup(balancer, action);

	if (vs == NULL) {
		// invalid configuration
		pipeline_packet_drop(pipeline, packet);
		return;
	}

	struct balancer_rs *rs = balancer_rs_lookup(balancer, vs, packet);
	if (rs == NULL) {
		// real lookup failed
		pipeline_packet_drop(pipeline, packet);
		return;
	}

	if (balancer_route(balancer, vs, rs, packet) != 0) {
		pipeline_packet_drop(pipeline, packet);
		return;
	}

	pipeline_packet_output(pipeline, packet);
}

static void
balancer_handle(struct module *module, struct pipeline *pipeline)
{
	struct balancer_module *balancer =
		container_of(module, struct balancer_module, module);

	struct packet_list input = pipeline_packet_input(pipeline);

	for (struct packet *packet == packet_list_first(&input);
	     packet != NULL;
	     packet = packet->next) {
		balancer_handle_packet(balancer, pipeline, packet);
	}
}


