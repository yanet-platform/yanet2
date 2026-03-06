#pragma once

#include "classifiers/segments.h"
#include "common/registry.h"

struct segment_u16 {
	uint16_t from;
	uint16_t to;
	uint32_t label;
};

// All equal segment labels must be consecutive.
// Classifier 0 matches no segments.
int
segments_classifier_u16_init(
	struct segment_u16_classifier *classifier,
	struct memory_context *mctx,
	struct value_registry *registry,
	size_t segment_count,
	struct segment_u16 *segments
);

void
segments_classifier_u16_free(
	struct segment_u16_classifier *classifier, struct memory_context *mctx
);

struct segment_u32 {
	uint32_t from;
	uint32_t to;
	uint32_t label;
};

// All equal segment labels must be consecutive.
// Classifier 0 matches no segments.
int
segments_classifier_u32_init(
	struct segments_u32_classifier *classifier,
	struct memory_context *mctx,
	struct value_registry *registry,
	size_t segment_count,
	struct segment_u32 *segments
);

void
segments_classifier_u32_free(
	struct segments_u32_classifier *classifier, struct memory_context *mctx
);

struct segment_u64 {
	uint64_t from;
	uint64_t to;
	uint32_t label;
};

// All equal segment labels must be consecutive.
int
segments_classifier_u64_init(
	struct segments_u64_classifier *classifier,
	struct memory_context *mctx,
	struct value_registry *registry,
	size_t segment_count,
	struct segment_u64 *segments
);

void
segments_classifier_u64_free(
	struct segments_u64_classifier *classifier, struct memory_context *mctx
);