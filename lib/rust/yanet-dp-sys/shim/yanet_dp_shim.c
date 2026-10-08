#include "yanet_dp_shim.h"

#include <rte_mbuf.h>

#include "lib/dataplane/module/packet_front.h"
#include "lib/dataplane/packet/data.h"
#include "lib/dataplane/packet/packet.h"

struct packet *
yanet_dp_shim_packet_front_pop_input(struct packet_front *packet_front) {
	return packet_list_pop(&packet_front->input);
}

void
yanet_dp_shim_packet_front_output(
	struct packet_front *packet_front, struct packet *packet
) {
	packet_front_output(packet_front, packet);
}

void
yanet_dp_shim_packet_front_drop(
	struct packet_front *packet_front, struct packet *packet
) {
	packet_front_drop(packet_front, packet);
}

uint8_t *
yanet_dp_shim_packet_data(struct packet *packet, uint16_t *data_len) {
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);
	*data_len = rte_pktmbuf_data_len(mbuf);
	return rte_pktmbuf_mtod(mbuf, uint8_t *);
}

uint32_t
yanet_dp_shim_packet_len(struct packet *packet) {
	return rte_pktmbuf_pkt_len(packet_to_mbuf(packet));
}

uint8_t *
yanet_dp_shim_packet_prepend(struct packet *packet, uint16_t len) {
	uint8_t *data =
		(uint8_t *)packet_headroom_prepend(packet_to_mbuf(packet), len);
	packet_refresh_data_len(packet);
	return data;
}

int
yanet_dp_shim_packet_adj(struct packet *packet, uint16_t len) {
	char *data = rte_pktmbuf_adj(packet_to_mbuf(packet), len);
	packet_refresh_data_len(packet);
	return data == NULL ? -1 : 0;
}

int
yanet_dp_shim_packet_parse(struct packet *packet) {
	return parse_packet(packet);
}
