#include "dpdk.h"

#include <stdarg.h>
#include <stdio.h>

#include <rte_dev.h>
#include <rte_eal.h>
#include <rte_ether.h>

#include <rte_ethdev.h>

#include "logging/log.h"

struct eal_args {
	char buf[1024];
	int buf_pos;
	char *argv[128];
	unsigned int argc;
};

static int __attribute__((format(printf, 2, 3)))
eal_args_add(struct eal_args *args, const char *fmt, ...) {
	if (args->argc >= sizeof(args->argv) / sizeof(args->argv[0]) - 1) {
		return -1;
	}

	int remaining = (int)sizeof(args->buf) - args->buf_pos;
	if (remaining <= 0) {
		return -1;
	}

	args->argv[args->argc++] = &args->buf[args->buf_pos];

	va_list ap;
	va_start(ap, fmt);
	int written = vsnprintf(&args->buf[args->buf_pos], remaining, fmt, ap);
	va_end(ap);

	if (written < 0 || written >= remaining) {
		return -1;
	}

	args->buf_pos += written + 1;
	return 0;
}

int
dpdk_init(
	const char *binary,
	uint64_t dpdk_memory,
	size_t port_count,
	const char *const *port_names
) {
	struct eal_args args = {0};

	if (eal_args_add(&args, "%s", binary)) {
		goto error;
	}

	for (size_t port_idx = 0; port_idx < port_count; ++port_idx) {
		if (eal_args_add(&args, "-a")) {
			goto error;
		}
		if (eal_args_add(&args, "%s", port_names[port_idx])) {
			goto error;
		}
	}

	if (eal_args_add(&args, "-m")) {
		goto error;
	}
	if (eal_args_add(&args, "%lu", dpdk_memory)) {
		goto error;
	}

	args.argv[args.argc] = NULL;

	return rte_eal_init(args.argc, args.argv);

error:
	LOG(ERROR, "EAL arguments buffer is full");
	return -1;
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
	port_conf.rxmode.offloads |= RTE_ETH_RX_OFFLOAD_TIMESTAMP;

	if ((rc = rte_eth_dev_configure(
		     *port_id, rx_queue_count, tx_queue_count, &port_conf
	     ))) {
		LOG(WARN,
		    "failed to configure port id %d (%s): %d",
		    *port_id,
		    name,
		    rc);
		LOG(INFO,
		    "try to configure port without RX timestamp offload %d "
		    "(%s)",
		    *port_id,
		    name);
		port_conf.rxmode.offloads &= ~RTE_ETH_RX_OFFLOAD_TIMESTAMP;

		if ((rc = rte_eth_dev_configure(
			     *port_id,
			     rx_queue_count,
			     tx_queue_count,
			     &port_conf
		     ))) {
			LOG(ERROR,
			    "failed to configure port id %d (%s): %d",
			    *port_id,
			    name,
			    rc);
			return -1;
		}
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
	rte_eth_promiscuous_enable(port_id);
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
