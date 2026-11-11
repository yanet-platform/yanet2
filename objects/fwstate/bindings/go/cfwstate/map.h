#pragma once

#include <errno.h>
#include <string.h>

#include "objects/fwstate/api/fwstate_map_v4_object.h"
#include "objects/fwstate/api/fwstate_map_v6_object.h"

static inline struct fwstate_stash *
cfwstate_map_stash(struct cp_object *object, bool ipv6) {
	return ipv6 ? &((struct fwstate_map_v6_object *)object)->stash
		    : &((struct fwstate_map_v4_object *)object)->stash;
}

// Validate the batch before changing either the round or its records.
static inline int
cfwstate_stash_append(
	struct fwstate_stash *stash,
	uint16_t worker,
	uint64_t iteration,
	const struct fw_state_sync_frame *frames,
	size_t count,
	bool ipv6
) {
	struct fwstate_stash_slot *slot = ADDR_OF(&stash->slots) + worker;
	uint32_t used = slot->iteration == iteration ? slot->count : 0;
	if (count > fwstate_stash_capacity(stash->size) - used) {
		return -ENOSPC;
	}
	for (size_t idx = 0; idx < count; ++idx) {
		if (frames[idx].addr_type != (ipv6 ? 6 : 4)) {
			return -EINVAL;
		}
	}
	fwstate_stash_slot_sync_round(slot, iteration);
	struct fwstate_sync_record *records = fwstate_stash_slot_records(slot);
	for (size_t idx = 0; idx < count; ++idx) {
		records[slot->count].frame = frames[idx];
		records[slot->count].status = FWSTATE_SYNC_RECORD_PENDING;
		fwstate_stash_slot_commit(slot);
	}
	return 0;
}

struct cfwstate_lookup_key {
	uint16_t protocol, source_port, destination_port;
	uint8_t source[16], destination[16];
};

struct cfwstate_lookup_result {
	struct fw_state_value value;
	uint64_t deadline;
	bool found;
};

static inline void
cfwstate_lookup(
	struct cp_object *object,
	bool ipv6,
	const struct cfwstate_lookup_key *key,
	struct cfwstate_lookup_result *result
) {
	struct fw4_state_key key4 = {0};
	struct fw6_state_key key6 = {0};
	void *lookup_key;
	if (ipv6) {
		key6.hdr.proto = key->protocol;
		key6.hdr.src_port = key->source_port;
		key6.hdr.dst_port = key->destination_port;
		memcpy(key6.src_addr, key->source, 16);
		memcpy(key6.dst_addr, key->destination, 16);
		lookup_key = &key6;
	} else {
		key4.hdr.proto = key->protocol;
		key4.hdr.src_port = key->source_port;
		key4.hdr.dst_port = key->destination_port;
		memcpy(&key4.src_addr, key->source, 4);
		memcpy(&key4.dst_addr, key->destination, 4);
		lookup_key = &key4;
	}
	fwtable_t *table = ipv6 ? fwstate_map_v6_object_table(object)
				: fwstate_map_v4_object_table(object);
	void *value = NULL;
	bool stale;
	*result = (struct cfwstate_lookup_result){0};
	if (fwtable_lookup_with_deadline(
		    table,
		    0,
		    lookup_key,
		    &value,
		    NULL,
		    &result->deadline,
		    &stale
	    ) >= 0) {
		result->found = true;
		result->value = *(struct fw_state_value *)value;
	}
}
