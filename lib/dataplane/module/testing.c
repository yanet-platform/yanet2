#include "testing.h"
#include "module.h"
#include <rte_mbuf.h>

static void
testing_init_mbuf(struct rte_mbuf *m, struct test_data data, uint16_t buf_len) {
	m->priv_size = 0;
	m->buf_len = buf_len;
	uint32_t mbuf_size = sizeof(struct rte_mbuf) + m->priv_size;

	/* start of buffer is after mbuf structure and priv data */
	m->buf_addr = (char *)m + mbuf_size;
	rte_mbuf_iova_set(m, rte_mempool_virt2iova(m) + mbuf_size);

	/* keep some headroom between start of buffer and data */
	m->data_off = RTE_MIN(RTE_PKTMBUF_HEADROOM, m->buf_len);

	/* init some constant fields */
	m->pool = NULL;
	m->nb_segs = 1;
	m->port = 1; // fix RTE_MBUF_PORT_INVALID;
	rte_mbuf_refcnt_set(m, 1);
	m->next = NULL;

	// Initialize mbuf data
	m->data_len = data.size;
	// TODO: multisegment packets
	m->pkt_len = (uint32_t)data.size;
	memcpy(rte_pktmbuf_mtod(m, uint8_t *), data.payload, data.size);
}

struct packet_front *
testing_packet_front(
	struct test_data payload[],
	uint8_t *arena,
	uint64_t arena_size,
	uint64_t mbuf_count,
	uint16_t mbuf_size
) {
	assert(arena_size >=
	       (sizeof(struct packet_front) + mbuf_size * mbuf_count));
	struct packet_front *pf = (struct packet_front *)(arena);
	packet_front_init(pf);
	arena += sizeof(struct packet_front);

	for (uint64_t i = 0; i < mbuf_count; i++) {
		struct rte_mbuf *m = (struct rte_mbuf *)(arena + mbuf_size * i);
		testing_init_mbuf(m, payload[i], mbuf_size);

		struct packet *p = mbuf_to_packet(m);
		// Initialize packet
		memset(p, 0, sizeof(struct packet));
		p->mbuf = m;
		p->rx_device_id = 0;
		p->tx_device_id = 0;
		packet_front_output(pf, p);
	}
	packet_front_switch(pf);
	return pf;
}

uint8_t *
testing_packet_data(const struct packet *p, uint16_t *len) {
	struct rte_mbuf *m = packet_to_mbuf(p);
	// TODO: multisegment packets
	*len = m->data_len;
	return rte_pktmbuf_mtod(m, uint8_t *);
}
