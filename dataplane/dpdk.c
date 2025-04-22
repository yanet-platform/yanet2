#include "dpdk.h"

#include <stdio.h>

#include <rte_dev.h>
#include <rte_eal.h>
#include <rte_ether.h>

#include <rte_ethdev.h>

#include "logging/log.h"

int
dpdk_init(
	const char *binary,
	uint64_t dpdk_memory,
	size_t port_count,
	const char *const *port_names
) {
	char buffer[1024];
	int bufferPosition = 0;

	unsigned int eal_argc = 0;
	char *eal_argv[128];
#define insert_eal_arg(args...)                                                \
	do {                                                                   \
		eal_argv[eal_argc++] = &buffer[bufferPosition];                \
		bufferPosition += snprintf(                                    \
			&buffer[bufferPosition],                               \
			sizeof(buffer) - bufferPosition,                       \
			##args                                                 \
		);                                                             \
		bufferPosition++;                                              \
	} while (0)

	insert_eal_arg("%s", binary);

	for (size_t port_idx = 0; port_idx < port_count; ++port_idx) {
		insert_eal_arg("-a");
		insert_eal_arg("%s", port_names[port_idx]);
	}

	insert_eal_arg("-m");
	insert_eal_arg("%lu", dpdk_memory);

	eal_argv[eal_argc] = NULL;

	return rte_eal_init(eal_argc, eal_argv);
}

int
dpdk_add_vdev_port(
	const char *port_name,
	const char *name,
	const char *mac_addr,
	uint16_t queue_count
) {
	char vdev_args[256];
	snprintf(
		vdev_args,
		sizeof(vdev_args),
		"path=/dev/vhost-net,queues=%d,queue_size=%d,iface=%s,mac=%s",
		queue_count,
		4096,
		name,
		mac_addr
	);

	return rte_eal_hotplug_add("vdev", port_name, vdev_args);
}

int
dpdk_port_init(
	const char *name,
	uint16_t *port_id,
	uint64_t rss_hash,
	uint16_t rx_queue_count,
	uint16_t tx_queue_count,
	uint16_t mtu,
	uint16_t max_lro_packet_size
) {
	int rc;
	if ((rc = rte_eth_dev_get_port_by_name(name, port_id))) {
		LOG(ERROR, "failed to get port id for %s: %d", name, rc);
		return -1;
	}

	struct rte_eth_conf port_conf;
	memset(&port_conf, 0, sizeof(struct rte_eth_conf));
	port_conf.rxmode.max_lro_pkt_size = max_lro_packet_size;

	if (rss_hash != 0) {
		port_conf.rxmode.mq_mode = RTE_ETH_MQ_RX_RSS;
		port_conf.rx_adv_conf.rss_conf.rss_hf = rss_hash;
	}

	if ((rc = rte_eth_dev_configure(
		     *port_id, rx_queue_count, tx_queue_count, &port_conf
	     ))) {
		LOG(ERROR,
		    "failed to configure port id %d (%s): %d",
		    *port_id,
		    name,
		    rc);
		return -1;
	}

	if ((rc = rte_eth_dev_set_mtu(*port_id, mtu))) {
		LOG(ERROR,
		    "failed to set mtu for port id %d (%s): %d",
		    *port_id,
		    name,
		    rc);
		return -1;
	}

	rte_eth_stats_reset(*port_id);
	rte_eth_xstats_reset(*port_id);

	return 0;
}

int
dpdk_port_start(uint16_t port_id) {
	return rte_eth_dev_start(port_id);
}

int
dpdk_port_stop(uint16_t port_id) {
	return rte_eth_dev_stop(port_id);
}

int
dpdk_port_get_mac(uint16_t port_id, struct rte_ether_addr *ether_addr) {
	return rte_eth_macaddr_get(port_id, ether_addr);
}
