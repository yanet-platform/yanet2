#include "config.h"

#include <rte_ether.h>

#include "common/container_of.h"

#include "dataplane/module/module.h"
#include "dataplane/packet/packet.h"
#include "dataplane/pipeline/pipeline.h"

static void
vlan_input_handle(
	struct dp_worker *dp_worker,
	struct device_ectx *device_ectx,
	struct packet *packet
) {
	(void)dp_worker;
	(void)device_ectx;
	(void)packet;
}

static void
vlan_output_handle(
	struct dp_worker *dp_worker,
	struct device_ectx *device_ectx,
	struct packet *packet
) {
	(void)dp_worker;

	struct cp_device *cp_device = ADDR_OF(&device_ectx->cp_device);
	struct cp_device_vlan *cp_device_vlan =
		container_of(cp_device, struct cp_device_vlan, cp_device);

	struct rte_mbuf *mbuf = packet_to_mbuf(packet);
	uint16_t offset = 0;
	if (rte_pktmbuf_pkt_len(mbuf) < sizeof(struct rte_ether_hdr)) {
		goto error_invalid;
	}

	struct rte_ether_hdr *ether_hdr =
		rte_pktmbuf_mtod_offset(mbuf, struct rte_ether_hdr *, 0);
	offset += sizeof(struct rte_ether_hdr);

	if (cp_device_vlan->vlan == 0) {
		if (ether_hdr->ether_type !=
		    rte_cpu_to_be_16(RTE_ETHER_TYPE_VLAN)) {
			/*
			 * No output tag is set for device and the packet does
			 * not have one - nothing to do.
			 */
			return;
		}

		/*
		 * We do not care about the header after the vlan one so just
		 * drop the vlan.
		 */
		if (rte_pktmbuf_pkt_len(mbuf) <
		    sizeof(struct rte_ether_hdr) + offset) {
			goto error_invalid;
		}

		struct rte_vlan_hdr *vlan_hdr = rte_pktmbuf_mtod_offset(
			mbuf, struct rte_vlan_hdr *, offset
		);
		ether_hdr->ether_type = vlan_hdr->eth_proto;

		memmove(rte_pktmbuf_mtod_offset(
				mbuf, char *, sizeof(struct rte_vlan_hdr)
			),
			rte_pktmbuf_mtod(mbuf, char *),
			sizeof(struct rte_ether_hdr));
		rte_pktmbuf_adj(mbuf, sizeof(struct rte_vlan_hdr));

		return;
	}

	// We have to set vlan tag
	if (ether_hdr->ether_type == rte_cpu_to_be_16(RTE_ETHER_TYPE_VLAN)) {
		if (rte_pktmbuf_pkt_len(mbuf) <
		    sizeof(struct rte_ether_hdr) + offset) {
			goto error_invalid;
		}

		struct rte_vlan_hdr *vlan_hdr = rte_pktmbuf_mtod_offset(
			mbuf, struct rte_vlan_hdr *, offset
		);

		// Just update the tag
		vlan_hdr->vlan_tci = rte_cpu_to_be_16(cp_device_vlan->vlan);

		return;
	}

	// Inject new vlan header
	// FIXME: check error
	rte_pktmbuf_prepend(mbuf, sizeof(struct rte_vlan_hdr));
	memmove(rte_pktmbuf_mtod(mbuf, char *),
		rte_pktmbuf_mtod_offset(
			mbuf, char *, sizeof(struct rte_vlan_hdr)
		),
		sizeof(struct rte_ether_hdr));

	ether_hdr = rte_pktmbuf_mtod_offset(mbuf, struct rte_ether_hdr *, 0);
	offset = sizeof(struct rte_ether_hdr);

	struct rte_vlan_hdr *vlan_hdr =
		rte_pktmbuf_mtod_offset(mbuf, struct rte_vlan_hdr *, offset);

	vlan_hdr->vlan_tci = rte_cpu_to_be_16(cp_device_vlan->vlan);
	vlan_hdr->eth_proto = ether_hdr->ether_type;
	ether_hdr->ether_type = rte_cpu_to_be_16(RTE_ETHER_TYPE_VLAN);

	return;

error_invalid:
	/*
	 * FIXME: device handler does not process packet front so
	 * it could not drop the packet so just ignore it
	 */
	return;
}

struct device_vlan {
	struct device device;
};

struct device *
new_device_vlan() {
	struct device_vlan *device_vlan =
		(struct device_vlan *)malloc(sizeof(struct device_vlan));

	if (device_vlan == NULL) {
		return NULL;
	}

	snprintf(
		device_vlan->device.name,
		sizeof(device_vlan->device.name),
		"%s",
		"vlan"
	);
	device_vlan->device.input_handler = vlan_input_handle;
	device_vlan->device.output_handler = vlan_output_handle;

	return &device_vlan->device;
}
