#pragma once

#include "common/btree/u16.h"
#include "common/btree/u32.h"
#include "common/btree/u64.h"

struct segment_u16_classifier {
	struct btree_u16 btree;
	uint32_t *open;
};

struct segments_u32_classifier {
	struct btree_u32 btree;
	uint32_t *open;
};

struct segments_u64_classifier {
	struct btree_u64 btree;
	uint32_t *open;
};
