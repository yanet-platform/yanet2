#pragma once

#include <lib/dataplane/module/module.h>

#include "../api/rule.h"
#include "rule.h"

#include <assert.h>
#include <stddef.h>
#include <stdint.h>

////////////////////////////////////////////////////////////////////////////////

/// @todo: fixme
#define DEVICE_COUNT 1

////////////////////////////////////////////////////////////////////////////////

static inline uint16_t
device_id(const char *device) {
	(void)device;

	/// @todo: fixme
	/// for now, there is only one device
	return 0;
}

////////////////////////////////////////////////////////////////////////////////

static inline int
filter_action_pack(
	enum acl_action action_kind,
	uint8_t flags,
	size_t device_count,
	const char **devices,
	uint32_t *result
) {
	/// @todo: get ids of the provided devices and make correct category
	/// mask based on it
	// for now, assume there is only one device
	uint16_t devices_mask = 0;
	for (size_t id = 0; id < device_count; ++id) {
		devices_mask |= 1 << device_id(devices[id]);
	}
	bool non_terminate_flag = (flags & ACL_RULE_NON_TERMINATE_FLAG) != 0;
	uint16_t action = action_kind | (((uint16_t)flags) << 7);
	*result =
		filter_action_create(devices_mask, non_terminate_flag, action);
	return 0;
}

static inline int
filter_action_unpack(
	uint32_t filter_action, enum acl_action *action_kind, uint8_t *flags
) {
	uint32_t action_kind_bits = filter_action & 0x7F;
	assert(action_kind_bits < acl_actions_count);
	*action_kind = *(enum acl_action *)&action_kind_bits;
	*flags = (filter_action >> 7) & 0xFF;
	return 0;
}

////////////////////////////////////////////////////////////////////////////////

static inline int
process_packet_action(
	enum acl_action action_kind,
	uint8_t flags,
	size_t device_id,
	struct packet *packet,
	struct packet_front *packet_front
) {
	(void)device_id;
	switch (action_kind) {
	case acl_action_pass: {
		// use device here?
		packet_front_output(packet_front, packet);

		// process flags here
		if (flags & ACL_RULE_LOG_FLAG) {
			// do something
		}
		break;
	}
	case acl_action_deny: {
		// user device here?
		packet_front_drop(packet_front, packet);

		// process flags here
		if (flags & ACL_RULE_LOG_FLAG) {
			// do something
		}
		break;
	}
	case acl_action_action_count:
		/// @todo
		break;
	case acl_action_check_state:
		/// @todo
		break;
	case acl_actions_count: {
		assert(0 && "impossible value: acl_actions_count");
	}
	default:
		assert(0 && "hit default value which is impossible");
	}
	return 0;
}

static inline int
process_packet_actions(
	uint32_t count,
	uint32_t *filter_actions,
	struct packet *packet,
	struct packet_front *packet_front
) {
	// bitmask of devices for which we did not meet terminate action
	uint16_t non_terminated_mask = ((1 << DEVICE_COUNT) - 1);
	for (uint32_t i = 0; i < count; ++i) {
		// get action kind and flags
		uint32_t filter_action = filter_actions[i];
		enum acl_action action_kind;
		uint8_t flags;
		filter_action_unpack(filter_action, &action_kind, &flags);

		// bitmask of devices for which current filter action should be
		// applied
		uint16_t device_mask =
			non_terminated_mask &
			FILTER_ACTION_CATEGORY_MASK(filter_action);
		for (size_t device_id = 0; device_id < DEVICE_COUNT;
		     ++device_id) {
			if (device_mask & (1 << device_id)) {
				process_packet_action(
					action_kind,
					flags,
					device_id,
					packet,
					packet_front
				);
			}
		}

		// recalculate bitmask of devices for which we are not
		// terminated still
		if (!(flags & ACL_RULE_NON_TERMINATE_FLAG)) {
			non_terminated_mask &= ~device_mask;
		}
	}
	return 0;
}

////////////////////////////////////////////////////////////////////////////////

static_assert(acl_actions_count <= 0x80, "too many acl actions");