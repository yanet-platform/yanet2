#include <stdio.h>

#include <dlfcn.h>
#include <string.h>

#include <sys/mman.h>

#include <pcap.h>

#include <rte_mbuf.h>

#include "api/agent.h"
#include "controlplane/agent/agent.h"
#include "controlplane/config/zone.h"
#include "dataplane/config/zone.h"
#include "dataplane/dpdk.h"
#include "dataplane/module/module.h"
#include "dataplane/pipeline/pipeline.h"
#include "modules/route/api/controlplane.h"

#include "common/malloc_heap.h"
#include "rte_memory.h"

static int
read_packets(struct rte_mempool *pool, struct packet_front *packet_front) {
	char pcap_errbuf[512];
	//	struct pcap *pcap = pcap_fopen_offline(stdin, pcap_errbuf);
	struct pcap *pcap =
		pcap_fopen_offline(fopen("001-send.pcap", "r"), pcap_errbuf);
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
dataplane_init_storage(
	const char *storage_name,

	size_t dp_memory,
	size_t cp_memory,

	struct dp_config **res_dp_config,
	struct cp_config **res_cp_config
);

int
dataplane_load_module(
	struct dp_config *dp_config, void *bin_hndl, const char *name
);

int
main(int argc, char **argv) {
	(void)argc;
	(void)argv;

	dpdk_init(argv[0], 32, 0, NULL);

	void *bin_hndl = dlopen(NULL, RTLD_NOW | RTLD_GLOBAL);

	struct dp_config *dp_config;
	struct cp_config *cp_config;

	dataplane_init_storage(
		"/tmp/unit", 1 << 24, 1 << 24, &dp_config, &cp_config
	);

	struct dp_worker dp_worker;
	dp_worker.idx = 0;

	dataplane_load_module(dp_config, bin_hndl, "route");

	struct yanet_shm *shm = yanet_shm_attach("/tmp/unit");
	if (shm == NULL) {
		printf("failed to attach shm: %s\n", strerror(errno));
		return -1;
	}

	struct agent *agent = agent_attach(shm, 0, "test", 1 << 20);
	struct cp_module *rmc = route_module_config_create(agent, "route0");
	route_module_config_add_route(
		rmc,
		(struct ether_addr){
			.addr = {0x01, 0x02, 0x03, 0x04, 0x05, 0x06},
		},
		(struct ether_addr){
			.addr = {0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c},
		}
	);

	route_module_config_add_route_list(rmc, 1, (uint32_t[]){0});

	route_module_config_add_prefix_v4(
		rmc,
		(uint8_t[4]){0, 0, 0, 0},
		(uint8_t[4]){0xff, 0xff, 0xff, 0xff},
		0
	);

	route_module_config_add_prefix_v6(
		rmc,
		(uint8_t[16]){0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
		(uint8_t[16]){0xff,
			      0xff,
			      0xff,
			      0xff,
			      0xff,
			      0xff,
			      0xff,
			      0xff,
			      0xff,
			      0xff,
			      0xff,
			      0xff,
			      0xff,
			      0xff,
			      0xff,
			      0xff},
		0
	);

	agent_update_modules(agent, 1, &rmc);

	struct pipeline_config *pc = pipeline_config_create("test", 1);
	pipeline_config_set_module(pc, 0, "route", "route0");
	agent_update_pipelines(agent, 1, &pc);

	/*struct malloc_heap heap;
	malloc_heap_create(&heap, "pituh");
	struct rte_memseg_list msl;
	memset(&msl, 0, sizeof(msl));
	msl.base_va = malloc(1 << 25);
	msl.len = 1 << 25;
	msl.page_sz = 4096;
	malloc_heap_add_external_memory(&heap, &msl);
	*/
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

	struct packet_front packet_front;
	packet_front_init(&packet_front);

	read_packets(pool, &packet_front),

		pipeline_process(
			dp_config,
			&dp_worker,
			ADDR_OF(&cp_config->cp_config_gen),
			0,
			&packet_front
		);

	write_packets(&packet_front);

	yanet_shm_detach(shm);

	return 0;
}
