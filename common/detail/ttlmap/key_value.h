#pragma once

#include "city.h"

////////////////////////////////////////////////////////////////////////////////

#define __TTLMAP_KEYS_EQUAL(key1_ptr, key2_ptr)                                \
	(memcmp(key1_ptr, key2_ptr, sizeof(typeof(*(key1_ptr)))) == 0)

#define __TTLMAP_KEY_HASH(key_ptr)                                             \
	city_hash32((const char *)(key_ptr), sizeof(typeof(*(key_ptr))));

#define __TTLMAP_MEMORY_SET(dst_ptr, src_ptr)                                  \
	memcpy(dst_ptr, src_ptr, sizeof(typeof(*(dst_ptr))))
