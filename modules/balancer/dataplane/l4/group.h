#pragma once

#include <stddef.h>
#include <stdint.h>

/*
 * Rearrange vs_ids[] and order[] so that entries with the same vs_id
 * are contiguous. Uses a two-pass counting sort with a small group
 * table, giving O(n * k) time for k distinct VS IDs.
 *
 * If there is too many distinct VS, the grouping is skipped.
 */
void
group_by_id(uint32_t *ids, uint8_t *order, size_t count);