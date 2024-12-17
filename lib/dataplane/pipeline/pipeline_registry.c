#include "pipeline_registry.h"

#include "dataplane/pipeline/pipeline.h"

#include <string.h>

int
pipeline_registry_register(
	struct pipeline_registry *pipeline_registry, struct pipeline *pipeline
) {

	for (uint32_t idx = 0; idx < pipeline_registry->pipeline_count; ++idx) {
		struct pipeline *known_pipeline =
			pipeline_registry->pipelines[idx];

		if (!strncmp(
			    known_pipeline->name,
			    pipeline->name,
			    PIPELINE_NAME_LEN
		    )) {
			// TODO: error code
			return -1;
		}
	}

	// Module is not known by pointer nor name

	// FIXME array extending as routine/library
	if (pipeline_registry->pipeline_count % 8 == 0) {
		struct pipeline **new_pipelines = (struct pipeline **)realloc(
			pipeline_registry->pipelines,
			sizeof(struct pipeline *) *
				(pipeline_registry->pipeline_count + 8)
		);
		if (new_pipelines == NULL) {
			// TODO: error code
			return -1;
		}
		pipeline_registry->pipelines = new_pipelines;
	}

	pipeline_registry->pipelines[pipeline_registry->pipeline_count++] =
		pipeline;

	return 0;
}

struct pipeline *
pipeline_registry_lookup(
	struct pipeline_registry *pipeline_registry, const char *pipeline_name
) {
	for (uint32_t idx = 0; idx < pipeline_registry->pipeline_count; ++idx) {
		struct pipeline *pipeline = pipeline_registry->pipelines[idx];

		if (!strncmp(
			    pipeline->name, pipeline_name, PIPELINE_NAME_LEN
		    )) {
			return pipeline;
		}
	}

	return NULL;
}
