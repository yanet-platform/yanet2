#include "segments.h"
#include "common/btree/u16.h"
#include "common/btree/u32.h"
#include "common/btree/u64.h"
#include "common/memory.h"
#include "common/memory_address.h"
#include "common/registry.h"
#include <stdlib.h>

enum point_type {
	open = 1,
	close = -1,
};

struct point {
	uint64_t x;
	enum point_type type;
};

static int
compare_points(const void *a, const void *b) {
	const struct point *pa = a;
	const struct point *pb = b;

	if (pa->x < pb->x) {
		return -1;
	}
	if (pa->x > pb->x) {
		return 1;
	}

	return 0;
}

// Count unique X coordinates in sorted points array
static size_t
count_unique_x_coords(struct point *points, size_t point_count) {
	size_t uniq_x = 0;
	for (size_t point_idx = 0; point_idx < point_count; ++point_idx) {
		if (point_idx == 0 ||
		    points[point_idx].x != points[point_idx - 1].x) {
			++uniq_x;
		}
	}
	return uniq_x;
}

// Validate that all segments have from <= to
static int
validate_segments_u16(size_t segment_count, struct segment_u16 *segments) {
	for (size_t segment_idx = 0; segment_idx < segment_count;
	     ++segment_idx) {
		if (segments[segment_idx].from > segments[segment_idx].to) {
			return -2;
		}
	}
	return 0;
}

// Create points array from segments (open/close points)
static struct point *
create_points_from_segments_u16(
	size_t segment_count, struct segment_u16 *segments, size_t *points_count
) {
	struct point *points = malloc(segment_count * 2 * sizeof(struct point));
	if (points == NULL) {
		return NULL;
	}

	size_t points_cnt = 0;

	for (size_t segment_idx = 0; segment_idx < segment_count;
	     ++segment_idx) {
		struct segment_u16 *segment = &segments[segment_idx];
		points[points_cnt++] = (struct point){
			.x = segment->from,
			.type = open,
		};
		if (segment->to < UINT16_MAX) {
			points[points_cnt++] = (struct point){
				.x = segment->to + 1,
				.type = close,
			};
		}
	}

	// sort points
	qsort(points, points_cnt, sizeof(struct point), compare_points);

	*points_count = points_cnt;

	return points;
}

// Allocate and fill x_coords and opens arrays
static int
fill_coords_and_opens_u16(
	struct segment_u16_classifier *classifier,
	struct memory_context *mctx,
	struct point *points,
	size_t point_count,
	size_t uniq_x,
	uint16_t **out_x_coords
) {
	uint16_t *x_coords = malloc(uniq_x * sizeof(uint16_t));
	if (x_coords == NULL && uniq_x > 0) {
		return -1;
	}

	uint32_t *opens = memory_balloc(mctx, uniq_x * sizeof(uint32_t));
	if (opens == NULL && uniq_x > 0) {
		free(x_coords);
		return -1;
	}

	ssize_t last_taken_idx = -1;
	int32_t cumulative_open = 0;
	for (size_t point_idx = 0; point_idx < point_count; ++point_idx) {
		struct point *cur_point = &points[point_idx];
		cumulative_open += cur_point->type;
		if (last_taken_idx >= 0 &&
		    cur_point->x == x_coords[last_taken_idx]) {
		} else {
			x_coords[++last_taken_idx] = cur_point->x;
		}
		opens[last_taken_idx] = cumulative_open;
	}

	// init btree
	if (btree_u16_init(
		    &classifier->btree, x_coords, last_taken_idx + 1, mctx
	    ) != 0) {
		memory_bfree(mctx, opens, uniq_x * sizeof(uint32_t));
		free(x_coords);
		return -1;
	}

	SET_OFFSET_OF(&classifier->open, opens);
	*out_x_coords = x_coords;

	return 0;
}

// Fill the value registry from segments
static int
fill_registry_from_segments_u16(
	struct segment_u16_classifier *classifier,
	struct value_registry *registry,
	size_t segment_count,
	struct segment_u16 *segments,
	uint16_t *x_coords
) {
	for (size_t segment_idx = 0; segment_idx < segment_count;
	     ++segment_idx) {
		if (segment_idx == 0 ||
		    segments[segment_idx].label !=
			    segments[segment_idx - 1].label) {
			if (value_registry_start(registry) != 0) {
				return -1;
			}
		}

		struct segment_u16 *segment = &segments[segment_idx];

		uint32_t idx = btree_u16_lower_bound(
			&classifier->btree, segment->from
		);
		while (idx < classifier->btree.n && x_coords[idx] <= segment->to
		) {
			if (value_registry_collect(registry, idx + 1) != 0) {
				return -1;
			}
			++idx;
		}
	}

	return 0;
}

int
segments_classifier_u16_init(
	struct segment_u16_classifier *classifier,
	struct memory_context *mctx,
	struct value_registry *registry,
	size_t segment_count,
	struct segment_u16 *segments
) {
	// validate
	if (validate_segments_u16(segment_count, segments) != 0) {
		return -2;
	}

	// create and sort points
	size_t points_cnt = 0;
	struct point *points = create_points_from_segments_u16(
		segment_count, segments, &points_cnt
	);
	if (points == NULL) {
		return -1;
	}

	// count unique coordinates
	size_t uniq_x = count_unique_x_coords(points, points_cnt);

	// fill coords and opens arrays, init btree
	uint16_t *x_coords;
	if (fill_coords_and_opens_u16(
		    classifier, mctx, points, points_cnt, uniq_x, &x_coords
	    ) != 0) {
		free(points);
		return -1;
	}

	// fill registry
	if (fill_registry_from_segments_u16(
		    classifier, registry, segment_count, segments, x_coords
	    ) != 0) {
		memory_bfree(
			mctx,
			ADDR_OF(&classifier->open),
			uniq_x * sizeof(uint32_t)
		);
		free(x_coords);
		free(points);
		return -1;
	}

	free(points);
	free(x_coords);

	return 0;
}

void
segments_classifier_u16_free(
	struct segment_u16_classifier *classifier, struct memory_context *mctx
) {
	btree_u16_free(&classifier->btree);
	memory_bfree(
		mctx,
		ADDR_OF(&classifier->open),
		classifier->btree.n * sizeof(uint32_t)
	);
}

// Validate that all segments have from <= to
static int
validate_segments_u32(size_t segment_count, struct segment_u32 *segments) {
	for (size_t segment_idx = 0; segment_idx < segment_count;
	     ++segment_idx) {
		if (segments[segment_idx].from > segments[segment_idx].to) {
			return -2;
		}
	}
	return 0;
}

// Create points array from segments (open/close points)
static struct point *
create_points_from_segments_u32(
	size_t segment_count, struct segment_u32 *segments, size_t *points_count
) {
	struct point *points = malloc(segment_count * 2 * sizeof(struct point));
	if (points == NULL) {
		return NULL;
	}

	size_t points_cnt = 0;

	for (size_t segment_idx = 0; segment_idx < segment_count;
	     ++segment_idx) {
		struct segment_u32 *segment = &segments[segment_idx];
		points[points_cnt++] = (struct point){
			.x = segment->from,
			.type = open,
		};
		if (segment->to < UINT32_MAX) {
			points[points_cnt++] = (struct point){
				.x = segment->to + 1,
				.type = close,
			};
		}
	}

	// sort points
	qsort(points, points_cnt, sizeof(struct point), compare_points);

	*points_count = points_cnt;

	return points;
}

// Allocate and fill x_coords and opens arrays
static int
fill_coords_and_opens_u32(
	struct segments_u32_classifier *classifier,
	struct memory_context *mctx,
	struct point *points,
	size_t point_count,
	size_t uniq_x,
	uint32_t **out_x_coords
) {
	uint32_t *x_coords = malloc(uniq_x * sizeof(uint32_t));
	if (x_coords == NULL && uniq_x > 0) {
		return -1;
	}

	uint32_t *opens = memory_balloc(mctx, uniq_x * sizeof(uint32_t));
	if (opens == NULL && uniq_x > 0) {
		free(x_coords);
		return -1;
	}

	ssize_t last_taken_idx = -1;
	int32_t cumulative_open = 0;
	for (size_t point_idx = 0; point_idx < point_count; ++point_idx) {
		struct point *cur_point = &points[point_idx];
		cumulative_open += cur_point->type;
		if (last_taken_idx >= 0 &&
		    cur_point->x == x_coords[last_taken_idx]) {
		} else {
			x_coords[++last_taken_idx] = cur_point->x;
		}
		opens[last_taken_idx] = cumulative_open;
	}

	// init btree
	if (btree_u32_init(
		    &classifier->btree, x_coords, last_taken_idx + 1, mctx
	    ) != 0) {
		memory_bfree(mctx, opens, uniq_x * sizeof(uint32_t));
		free(x_coords);
		return -1;
	}

	SET_OFFSET_OF(&classifier->open, opens);
	*out_x_coords = x_coords;

	return 0;
}

// Fill the value registry from segments
static int
fill_registry_from_segments_u32(
	struct segments_u32_classifier *classifier,
	struct value_registry *registry,
	size_t segment_count,
	struct segment_u32 *segments,
	uint32_t *x_coords
) {
	for (size_t segment_idx = 0; segment_idx < segment_count;
	     ++segment_idx) {
		if (segment_idx == 0 ||
		    segments[segment_idx].label !=
			    segments[segment_idx - 1].label) {
			if (value_registry_start(registry) != 0) {
				return -1;
			}
		}

		struct segment_u32 *segment = &segments[segment_idx];

		uint32_t idx = btree_u32_lower_bound(
			&classifier->btree, segment->from
		);
		while (idx < classifier->btree.n && x_coords[idx] <= segment->to
		) {
			if (value_registry_collect(registry, idx + 1) != 0) {
				return -1;
			}
			++idx;
		}
	}

	return 0;
}

int
segments_classifier_u32_init(
	struct segments_u32_classifier *classifier,
	struct memory_context *mctx,
	struct value_registry *registry,
	size_t segment_count,
	struct segment_u32 *segments
) {
	// validate
	if (validate_segments_u32(segment_count, segments) != 0) {
		return -2;
	}

	// create and sort points
	size_t points_cnt = 0;
	struct point *points = create_points_from_segments_u32(
		segment_count, segments, &points_cnt
	);
	if (points == NULL) {
		return -1;
	}

	// count unique coordinates
	size_t uniq_x = count_unique_x_coords(points, points_cnt);

	// fill coords and opens arrays, init btree
	uint32_t *x_coords;
	if (fill_coords_and_opens_u32(
		    classifier, mctx, points, points_cnt, uniq_x, &x_coords
	    ) != 0) {
		free(points);
		return -1;
	}

	// fill registry
	if (fill_registry_from_segments_u32(
		    classifier, registry, segment_count, segments, x_coords
	    ) != 0) {
		memory_bfree(
			mctx,
			ADDR_OF(&classifier->open),
			uniq_x * sizeof(uint32_t)
		);
		free(x_coords);
		free(points);
		return -1;
	}

	free(points);
	free(x_coords);

	return 0;
}

void
segments_classifier_u32_free(
	struct segments_u32_classifier *classifier, struct memory_context *mctx
) {
	btree_u32_free(&classifier->btree);
	memory_bfree(
		mctx,
		ADDR_OF(&classifier->open),
		classifier->btree.n * sizeof(uint32_t)
	);
}

// Validate that all segments have from <= to
static int
validate_segments_u64(size_t segment_count, struct segment_u64 *segments) {
	for (size_t segment_idx = 0; segment_idx < segment_count;
	     ++segment_idx) {
		if (segments[segment_idx].from > segments[segment_idx].to) {
			return -2;
		}
	}
	return 0;
}

// Create points array from segments (open/close points)
static struct point *
create_points_from_segments_u64(
	size_t segment_count, struct segment_u64 *segments, size_t *points_count
) {
	struct point *points = malloc(segment_count * 2 * sizeof(struct point));
	if (points == NULL) {
		return NULL;
	}

	size_t points_cnt = 0;

	for (size_t segment_idx = 0; segment_idx < segment_count;
	     ++segment_idx) {
		struct segment_u64 *segment = &segments[segment_idx];
		points[points_cnt++] = (struct point){
			.x = segment->from,
			.type = open,
		};

		if (segment->to < UINT64_MAX) {
			points[points_cnt++] = (struct point){
				.x = segment->to + 1,
				.type = close,
			};
		}
	}

	// sort points
	qsort(points, points_cnt, sizeof(struct point), compare_points);

	*points_count = points_cnt;

	return points;
}

// Allocate and fill x_coords and opens arrays
static int
fill_coords_and_opens_u64(
	struct segments_u64_classifier *classifier,
	struct memory_context *mctx,
	struct point *points,
	size_t point_count,
	size_t uniq_x,
	uint64_t **out_x_coords
) {
	uint64_t *x_coords = malloc(uniq_x * sizeof(uint64_t));
	if (x_coords == NULL && uniq_x > 0) {
		return -1;
	}

	uint32_t *opens = memory_balloc(mctx, uniq_x * sizeof(uint32_t));
	if (opens == NULL && uniq_x > 0) {
		free(x_coords);
		return -1;
	}

	ssize_t last_taken_idx = -1;
	int32_t cumulative_open = 0;
	for (size_t point_idx = 0; point_idx < point_count; ++point_idx) {
		struct point *cur_point = &points[point_idx];
		cumulative_open += cur_point->type;
		if (last_taken_idx >= 0 &&
		    cur_point->x == x_coords[last_taken_idx]) {
		} else {
			x_coords[++last_taken_idx] = cur_point->x;
		}
		opens[last_taken_idx] = cumulative_open;
	}

	// init btree
	if (btree_u64_init(
		    &classifier->btree, x_coords, last_taken_idx + 1, mctx
	    ) != 0) {
		memory_bfree(mctx, opens, uniq_x * sizeof(uint32_t));
		free(x_coords);
		return -1;
	}

	SET_OFFSET_OF(&classifier->open, opens);
	*out_x_coords = x_coords;

	return 0;
}

// Fill the value registry from segments
static int
fill_registry_from_segments_u64(
	struct segments_u64_classifier *classifier,
	struct value_registry *registry,
	size_t segment_count,
	struct segment_u64 *segments,
	uint64_t *x_coords
) {
	for (size_t segment_idx = 0; segment_idx < segment_count;
	     ++segment_idx) {
		if (segment_idx == 0 ||
		    segments[segment_idx].label !=
			    segments[segment_idx - 1].label) {
			if (value_registry_start(registry) != 0) {
				return -1;
			}
		}

		struct segment_u64 *segment = &segments[segment_idx];

		uint32_t idx = btree_u64_lower_bound(
			&classifier->btree, segment->from
		);
		while (idx < classifier->btree.n && x_coords[idx] <= segment->to
		) {
			if (value_registry_collect(registry, idx + 1) != 0) {
				return -1;
			}
			++idx;
		}
	}

	return 0;
}

int
segments_classifier_u64_init(
	struct segments_u64_classifier *classifier,
	struct memory_context *mctx,
	struct value_registry *registry,
	size_t segment_count,
	struct segment_u64 *segments
) {
	// validate
	if (validate_segments_u64(segment_count, segments) != 0) {
		return -2;
	}

	// create and sort points
	size_t points_cnt = 0;
	struct point *points = create_points_from_segments_u64(
		segment_count, segments, &points_cnt
	);
	if (points == NULL) {
		return -1;
	}

	// count unique coordinates
	size_t uniq_x = count_unique_x_coords(points, points_cnt);

	// fill coords and opens arrays, init btree
	uint64_t *x_coords;
	if (fill_coords_and_opens_u64(
		    classifier, mctx, points, points_cnt, uniq_x, &x_coords
	    ) != 0) {
		free(points);
		return -1;
	}

	// fill registry
	if (fill_registry_from_segments_u64(
		    classifier, registry, segment_count, segments, x_coords
	    ) != 0) {
		memory_bfree(
			mctx,
			ADDR_OF(&classifier->open),
			uniq_x * sizeof(uint32_t)
		);
		free(x_coords);
		free(points);
		return -1;
	}

	free(points);
	free(x_coords);

	return 0;
}

void
segments_classifier_u64_free(
	struct segments_u64_classifier *classifier, struct memory_context *mctx
) {
	btree_u64_free(&classifier->btree);
	memory_bfree(
		mctx,
		ADDR_OF(&classifier->open),
		classifier->btree.n * sizeof(uint32_t)
	);
}
