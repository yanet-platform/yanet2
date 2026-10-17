// Packets on plain heap memory and a single-module front, for running the C
// and the Rust decap handler on identical input.
//
// Each packet owns one allocation holding its mbuf and the mbuf buffer; the
// packet descriptor lives at the buffer start like on the real receive path,
// and the frame is parsed with the dataplane's own parser.

#include <stdlib.h>
#include <string.h>

#include <rte_mbuf.h>

#include "lib/dataplane/module/module.h"
#include "lib/dataplane/module/packet_front.h"
#include "lib/dataplane/packet/data.h"
#include "lib/dataplane/packet/packet.h"
#include "lib/dataplane/pipeline/econtext.h"

#define TK_BUF_SIZE 4096

struct tk_mbuf {
	struct rte_mbuf mbuf;
	uint8_t buf[TK_BUF_SIZE] __attribute__((aligned(64)));
};

// Output and drop counters of the front after a handler ran.
struct tk_front_totals {
	uint64_t output_count;
	uint64_t output_bytes;
	uint64_t drop_count;
	uint64_t drop_bytes;
};

// Snapshot of the packet state a handler may change.
struct tk_packet_state {
	uint16_t data_len;
	uint16_t network_type;
	uint16_t network_offset;
	uint16_t transport_type;
	uint16_t transport_offset;
	uint16_t flags;
	uint32_t flow_label;
};

void
decap_handle_packets(
	struct dp_worker *dp_worker,
	struct module_ectx *module_ectx,
	struct packet_front *packet_front
);

module_handler
tk_c_decap_handler(void) {
	return decap_handle_packets;
}

struct packet *
tk_packet_new(const uint8_t *frame, uint16_t len) {
	if (len > TK_BUF_SIZE - RTE_PKTMBUF_HEADROOM) {
		return NULL;
	}
	struct tk_mbuf *tk = aligned_alloc(64, sizeof(*tk));
	if (tk == NULL) {
		return NULL;
	}
	memset(tk, 0, sizeof(*tk));

	struct rte_mbuf *mbuf = &tk->mbuf;
	mbuf->buf_addr = tk->buf;
	mbuf->buf_len = TK_BUF_SIZE;
	mbuf->data_off = RTE_PKTMBUF_HEADROOM;
	mbuf->nb_segs = 1;
	mbuf->pkt_len = len;
	mbuf->data_len = len;
	memcpy(tk->buf + RTE_PKTMBUF_HEADROOM, frame, len);

	struct packet *packet = mbuf_to_packet(mbuf);
	packet->mbuf = mbuf;
	if (parse_packet(packet)) {
		free(tk);
		return NULL;
	}
	return packet;
}

void
tk_packet_free(struct packet *packet) {
	if (packet != NULL) {
		free(packet->mbuf);
	}
}

const uint8_t *
tk_packet_data(struct packet *packet, uint16_t *len) {
	*len = packet_data_len(packet);
	return packet_data(packet);
}

void
tk_packet_state(const struct packet *packet, struct tk_packet_state *state) {
	state->data_len = packet->data_len;
	state->network_type = packet->network_header.type;
	state->network_offset = packet->network_header.offset;
	state->transport_type = packet->transport_header.type;
	state->transport_offset = packet->transport_header.offset;
	state->flags = packet->flags;
	state->flow_label = packet->flow_label;
}

// Runs one handler over the packets; verdicts are 1 for output, 2 for drop.
//
// Returns -1 when the handler lost or duplicated a packet.
int
tk_run_handler(
	module_handler handler,
	struct cp_module *cp_module,
	struct packet **packets,
	size_t count,
	uint8_t *verdicts,
	struct tk_front_totals *totals
) {
	struct module_ectx *ectx = calloc(1, sizeof(*ectx));
	if (ectx == NULL) {
		return -1;
	}
	ectx->abs_cp_module = cp_module;

	struct packet_front front;
	packet_front_init(&front);
	for (size_t i = 0; i < count; ++i) {
		verdicts[i] = 0;
		packet_front_input(&front, packets[i]);
	}

	handler(NULL, ectx, &front);
	free(ectx);

	size_t seen = 0;
	for (struct packet *p = front.output.first; p != NULL; p = p->next) {
		for (size_t i = 0; i < count; ++i) {
			if (packets[i] == p) {
				verdicts[i] = 1;
				++seen;
			}
		}
	}
	for (struct packet *p = front.drop.first; p != NULL; p = p->next) {
		for (size_t i = 0; i < count; ++i) {
			if (packets[i] == p) {
				verdicts[i] = 2;
				++seen;
			}
		}
	}
	totals->output_count = front.output_count;
	totals->output_bytes = front.output_bytes;
	totals->drop_count = front.drop_count;
	totals->drop_bytes = front.drop_bytes;
	if (seen != count || front.input.first != NULL ||
	    front.output_count + front.drop_count != count) {
		return -1;
	}
	return 0;
}
