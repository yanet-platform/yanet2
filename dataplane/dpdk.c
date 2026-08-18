#include "dpdk.h"

#include <stdarg.h>
#include <stdio.h>
#include <stdlib.h>

#include <rte_dev.h>
#include <rte_eal.h>
#include <rte_errno.h>
#include <rte_ether.h>

#include <rte_eth_ring.h>
#include <rte_ethdev.h>

#include "lib/logging/log.h"

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
	const char *iova_mode,
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

	// Explicitly pin the IOVA mode when requested via configuration.
	// This is required inside VMs without an IOMMU, where devices are
	// bound to vfio-pci in no-iommu mode (or uio): such devices need
	// IOVA as physical addresses ("pa"), but EAL may otherwise pick
	// "va" (e.g. because of a virtio_user/vhost-net port), which makes
	// the no-iommu PCI device fail to probe.
	if (iova_mode != NULL && iova_mode[0] != '\0') {
		LOG(INFO, "force DPDK IOVA mode to '%s'", iova_mode);
		if (eal_args_add(&args, "--iova-mode=%s", iova_mode)) {
			goto error;
		}
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
	uint16_t queue_count,
	uint16_t queue_size
) {
	char vdev_args[256];
	snprintf(
		vdev_args,
		sizeof(vdev_args),
		"path=/dev/vhost-net,queues=%d,queue_size=%d,iface=%s,mac=%s",
		queue_count,
		queue_size,
		name,
		mac_addr
	);

	return rte_eal_hotplug_add("vdev", port_name, vdev_args);
}

int
dpdk_add_ring_port(const char *port_name) {
	const char *parse = port_name + strlen("net_ring_:");
	const char *split = strchr(parse, ':');
	if (split == NULL) {
		LOG(ERROR, "invalid ring dev port format");
		return -1;
	}

	uint32_t socket_id = strtol(parse, NULL, 10);

	parse = split + 1;
	split = strchr(parse, ':');
	if (split == NULL) {
		LOG(ERROR, "invalid ring dev port format");
		return -1;
	}

	char rx_name[split - parse + 1];
	strncpy(rx_name, parse, split - parse);
	rx_name[split - parse] = 0;
	struct rte_ring *rx_ring = rte_ring_lookup(rx_name);
	if (rx_ring == NULL) {
		LOG(ERROR, "unknown ring %s", rx_name);
		return -1;
	}

	const char *tx_name = split + 1;
	struct rte_ring *tx_ring = rte_ring_lookup(tx_name);
	if (tx_ring == NULL) {
		LOG(ERROR, "unknown ring %s", tx_name);
		return -1;
	}

	if (rte_eth_from_rings(
		    port_name + strlen("net_ring_"),
		    &rx_ring,
		    1,
		    &tx_ring,
		    1,
		    socket_id
	    ) < 0) {
		LOG(ERROR,
		    "failed to create eth device from rings for %s: %s",
		    port_name,
		    rte_strerror(rte_errno));
		return -1;
	}

	return 0;
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

	if (mtu && (rc = rte_eth_dev_set_mtu(*port_id, mtu))) {
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

int
dpdk_port_get_rss_key(
	uint16_t port_id,
	uint8_t *key_buf,
	uint16_t key_buf_len,
	uint16_t *key_len
) {
	struct rte_eth_dev_info dev_info;
	memset(&dev_info, 0, sizeof(dev_info));
	if (rte_eth_dev_info_get(port_id, &dev_info)) {
		return -1;
	}
	if (dev_info.hash_key_size == 0 ||
	    dev_info.hash_key_size > key_buf_len) {
		return -1;
	}

	struct rte_eth_rss_conf rss_conf;
	memset(&rss_conf, 0, sizeof(rss_conf));
	rss_conf.rss_key = key_buf;
	rss_conf.rss_key_len = dev_info.hash_key_size;

	if (rte_eth_dev_rss_hash_conf_get(port_id, &rss_conf)) {
		return -1;
	}

	// A driver can answer the hash-config query even when RSS is not
	// active on the port (RSS disabled at configuration time, an
	// un-negotiated virtio feature, a ring device). An all-zero rss_hf
	// means the NIC is not steering traffic by RSS, so the key and RETA
	// it would report describe no usable RSS state.
	if (rss_conf.rss_hf == 0) {
		return -1;
	}

	*key_len = dev_info.hash_key_size;
	return 0;
}

int
dpdk_port_get_rss_reta(
	uint16_t port_id,
	uint16_t *reta_buf,
	uint16_t reta_buf_len,
	uint16_t *reta_size
) {
	struct rte_eth_dev_info dev_info;
	memset(&dev_info, 0, sizeof(dev_info));
	if (rte_eth_dev_info_get(port_id, &dev_info)) {
		return -1;
	}
	if (dev_info.reta_size == 0 || dev_info.reta_size > reta_buf_len ||
	    dev_info.reta_size > RTE_ETH_RSS_RETA_SIZE_512) {
		return -1;
	}

	// RETA entries are queried in fixed-size groups of
	// RTE_ETH_RETA_GROUP_SIZE. RTE_ETH_RSS_RETA_SIZE_512 is the largest
	// table size any driver reports today, so a group array sized for it
	// covers every real device without a dynamic allocation.
	uint16_t group_count =
		(dev_info.reta_size + RTE_ETH_RETA_GROUP_SIZE - 1) /
		RTE_ETH_RETA_GROUP_SIZE;
	struct rte_eth_rss_reta_entry64
		reta_conf[RTE_ETH_RSS_RETA_SIZE_512 / RTE_ETH_RETA_GROUP_SIZE];
	memset(reta_conf,
	       0,
	       sizeof(struct rte_eth_rss_reta_entry64) * group_count);
	for (uint16_t group = 0; group < group_count; ++group) {
		reta_conf[group].mask = UINT64_MAX;
	}

	if (rte_eth_dev_rss_reta_query(
		    port_id, reta_conf, dev_info.reta_size
	    )) {
		return -1;
	}

	for (uint16_t idx = 0; idx < dev_info.reta_size; ++idx) {
		uint16_t group = idx / RTE_ETH_RETA_GROUP_SIZE;
		uint16_t slot = idx % RTE_ETH_RETA_GROUP_SIZE;
		reta_buf[idx] = reta_conf[group].reta[slot];
	}

	*reta_size = dev_info.reta_size;
	return 0;
}
