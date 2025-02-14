#include <stdio.h>

#include <dlfcn.h>
#include <string.h>

#include <pcap.h>

#include <rte_mbuf.h>

#include "dataplane/config/dataplane_registry.h"
#include "dataplane/module/module.h"
#include "dataplane/pipeline/pipeline.h"

#include "dataplane/dpdk.h"

static int
read_packets(struct rte_mempool *pool, struct packet_front *packet_front) {
	char pcap_errbuf[512];
	struct pcap *pcap = pcap_fopen_offline(stdin, pcap_errbuf);
	if (!pcap) {
		fprintf(stderr, "pcap_fopen_offline(): %s\n", pcap_errbuf);
		return -1;
	}

	struct pcap_pkthdr *header;
	const u_char *data;

	while (pcap_next_ex(pcap, &header, &data) >= 0) {
		struct rte_mbuf *mbuf;
		mbuf = rte_pktmbuf_alloc(pool);

		rte_memcpy(
			rte_pktmbuf_mtod(mbuf, void *), data, header->caplen
		);
		mbuf->data_len = header->caplen;
		mbuf->pkt_len = mbuf->data_len;
		mbuf->port = 0;

		struct packet *packet = mbuf_to_packet(mbuf);
		memset(packet, 0, sizeof(struct packet));
		packet->mbuf = mbuf;
		packet->rx_device_id = 0;
		packet->tx_device_id = 0;
		parse_packet(packet);

		packet_front_output(packet_front, packet);
	}

	pcap_close(pcap);

	return 0;
}

static int
write_packets(struct packet_front *packet_front) {
	struct pcap *pcap = pcap_open_dead(DLT_EN10MB, 8192);
	struct pcap_dumper *dmp = pcap_dump_fopen(pcap, stdout);

	char write_buf[8192];

	struct packet *packet;
	while ((packet = packet_list_pop(&packet_front->output)) != NULL) {
		struct rte_mbuf *mbuf = packet_to_mbuf(packet);

		struct pcap_pkthdr header;
		header.len = rte_pktmbuf_pkt_len(mbuf);
		header.caplen = header.len;

		pcap_dump(
			(unsigned char *)dmp,
			&header,
			(void *)rte_pktmbuf_read(mbuf, 0, header.len, write_buf)
		);

		rte_pktmbuf_free(mbuf);
	}

	pcap_dump_close(dmp);
	pcap_close(pcap);

	(void)packet_front;

	return 0;
}

int
main(int argc, char **argv) {
	(void)argc;
	(void)argv;

	void *bin_hndl = dlopen(NULL, RTLD_NOW | RTLD_GLOBAL);

	dpdk_init(argv[0], 0, NULL);

	struct dataplane_registry config;
	dataplane_registry_init(&config);

	dataplane_registry_load_module(&config, bin_hndl, "route");

	struct rte_mempool *pool;
	pool = rte_mempool_create(
		"input",
		4096,
		8192,
		0,
		sizeof(struct rte_pktmbuf_pool_private),
		rte_pktmbuf_pool_init,
		NULL,
		rte_pktmbuf_init,
		NULL,
		0,
		MEMPOOL_F_SP_PUT | MEMPOOL_F_SC_GET
	);

	struct dataplane_module_config *dmc = (struct dataplane_module_config[]
	){{"route", "route0", NULL, 0}};
	struct dataplane_pipeline_config *dpc =
		(struct dataplane_pipeline_config[]
		){{"default",
		   (struct dataplane_pipeline_module[]){{"route", "route0"}},
		   1}};

	dataplane_registry_update(&config, dmc, 1, dpc, 1);

	struct pipeline *pipeline;
	pipeline =
		pipeline_registry_lookup(&config.pipeline_registry, "default");

	struct packet_front packet_front;
	packet_front_init(&packet_front);

	read_packets(pool, &packet_front),

		pipeline_process(pipeline, &packet_front);

	write_packets(&packet_front);

	return 0;
}
