#pragma once

#include "config.h"
#include "stash.h"
#include "types.h"
#include <stdbool.h>
#include <stdint.h>

// Forward declarations
struct dp_worker;
struct packet;

enum sync_packet_direction {
	// NOLINTBEGIN(readability-identifier-naming)
	SYNC_NONE,
	SYNC_INGRESS,
	SYNC_EGRESS,
	// NOLINTEND(readability-identifier-naming)
};

static inline bool
fwstate_sync_multicast_enabled(const struct fwstate_sync_config *config) {
	return config->port_multicast != 0;
}

static inline bool
fwstate_sync_unicast_enabled(const struct fwstate_sync_config *config) {
	return config->port_unicast != 0;
}

// Wire overhead of one sync packet on top of its frames: the IPv6 and UDP
// headers counted by the sync MTU.
#define FWSTATE_SYNC_IP_UDP_OVERHEAD 48

// Sync MTU used when the configuration leaves it zero.
#define FWSTATE_SYNC_DEFAULT_MTU 1500

// Smallest sync MTU that still carries one frame.
#define FWSTATE_SYNC_MIN_MTU                                                   \
	(FWSTATE_SYNC_IP_UDP_OVERHEAD + sizeof(struct fw_state_sync_frame))

// Largest number of frames one sync packet carries under the sync MTU.
//
// Zero selects the default MTU; a nonzero MTU too small for one frame,
// which the control plane refuses, still carries one.
static inline uint32_t
fwstate_sync_frames_per_packet(uint16_t sync_mtu) {
	uint32_t mtu = sync_mtu != 0 ? sync_mtu : FWSTATE_SYNC_DEFAULT_MTU;
	if (mtu < FWSTATE_SYNC_MIN_MTU) {
		return 1;
	}
	return (mtu - FWSTATE_SYNC_IP_UDP_OVERHEAD) /
	       sizeof(struct fw_state_sync_frame);
}

/**
 * Fill a stash record from the given packet.
 *
 * Captures the normalized 5-tuple, direction, current TCP flags and IPv6
 * flow id together with the packet's device ids, and marks the record
 * pending.
 *
 * @param packet The original packet to extract the 5-tuple from
 * @param direction The direction of the sync event (INGRESS or EGRESS)
 * @param record Record to fill
 * @return 0 on success, or -1 when the packet has no transport header
 */
int
fwstate_fill_sync_record(
	const struct packet *packet,
	const enum sync_packet_direction direction,
	struct fwstate_sync_record *record
);

/**
 * Build a destination-neutral sync packet carrying several frames.
 *
 * Writes the Ethernet, VLAN, IPv6 and UDP headers followed by the frames,
 * as many as the mbuf tailroom holds. The outer addresses stay zero until
 * fwstate_sync_set_destination.
 *
 * @param frames Frames to copy, in order
 * @param count Number of frames
 * @param rx_device_id Receive device of the sync packet
 * @param tx_device_id Transmit device of the sync packet
 * @param sync_pkt Freshly allocated packet to fill
 * @return the number of frames written, or -1 when not even one fits
 */
int
fwstate_build_sync_packet(
	const struct fw_state_sync_frame *const *frames,
	uint32_t count,
	uint16_t rx_device_id,
	uint16_t tx_device_id,
	struct packet *sync_pkt
);

// Outer wire addressing is assigned only after local state accepts the event.
void
fwstate_sync_set_destination(
	struct packet *packet,
	const struct ether_addr *dst_ether,
	const uint8_t dst_addr[16],
	uint16_t dst_port
);
