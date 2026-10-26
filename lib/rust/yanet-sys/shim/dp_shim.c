#include "dp_shim.h"

// The size tripwires every plugin compiles, so a plugin struct resized
// without a YANET_MODULE_ABI_VERSION bump fails this build too.
#include "lib/dataplane/config/plugin_abi_assert.h" // IWYU pragma: keep

#include "lib/dataplane/module/packet_front.h"
#include "lib/dataplane/packet/data.h"
#include "lib/dataplane/packet/decap.h"
#include "lib/dataplane/packet/encap.h"
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

char *
yanet_sys_dp_headroom_prepend(struct rte_mbuf *mbuf, uint16_t len) {
	return packet_headroom_prepend(mbuf, len);
}

void
yanet_sys_dp_front_pass(struct packet_front *packet_front) {
	packet_front_pass(packet_front);
}

// The Rust `DscpMark` carries these values.
_Static_assert(
	DSCP_MARK_NEVER == 0 && DSCP_MARK_DEFAULT == 1 && DSCP_MARK_ALWAYS == 2,
	"DSCP_MARK_* values changed"
);

int
yanet_sys_dp_ip4_encap(
	struct packet *packet,
	const uint8_t *dst,
	const uint8_t *src,
	uint8_t dscp_flag
) {
	return packet_ip4_encap(packet, dst, src, dscp_flag);
}

int
yanet_sys_dp_ip6_encap(
	struct packet *packet,
	const uint8_t *dst,
	const uint8_t *src,
	uint8_t dscp_flag
) {
	return packet_ip6_encap(packet, dst, src, dscp_flag);
}

int
yanet_sys_dp_mpls_encap(
	struct packet *packet,
	uint32_t label,
	uint8_t tc,
	uint8_t bottom,
	uint8_t ttl
) {
	return packet_mpls_encap(packet, label, tc, bottom, ttl);
}

int
yanet_sys_dp_ip4_encap_udp(
	struct packet *packet,
	const uint8_t *src,
	const uint8_t *dst,
	uint16_t src_port,
	uint16_t dst_port
) {
	const uint8_t ports[4] = {
		src_port >> 8, src_port & 0xff, dst_port >> 8, dst_port & 0xff
	};
	return packet_ip4_encap_udp(packet, src, dst, ports, ports + 2);
}

int
yanet_sys_dp_ip6_encap_udp(
	struct packet *packet,
	const uint8_t *src,
	const uint8_t *dst,
	uint16_t src_port,
	uint16_t dst_port
) {
	const uint8_t ports[4] = {
		src_port >> 8, src_port & 0xff, dst_port >> 8, dst_port & 0xff
	};
	return packet_ip6_encap_udp(packet, src, dst, ports, ports + 2);
}

int
yanet_sys_dp_decap(struct packet *packet) {
	return packet_decap(packet);
}
