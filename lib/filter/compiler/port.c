#include "common/memory.h"
#include "common/registry.h"
#include "common/value.h"
#include "declare.h"
#include "lib/filter/rule.h"

#include <stdint.h>

typedef void (*action_get_port_range_func)(
	const struct filter_rule *action,
	struct filter_port_range **ranges,
	uint32_t *count
);

static int
collect_port_values(
	struct memory_context *memory_context,
	const struct filter_rule **actions,
	uint32_t count,
	action_get_port_range_func get_port_range,
	struct vline *line,
	struct value_registry *registry
) {
	if (vline_init(line, memory_context, "port", 65536)) {
		return -1;
	}

	struct remap_table remap_table;
	if (remap_table_init(&remap_table, memory_context, 65536)) {
		goto error_remap_table;
	}

	for (const struct filter_rule **action_ptr = actions;
	     action_ptr < actions + count;
	     ++action_ptr) {

		remap_table_new_gen(&remap_table);

		if (*action_ptr == NULL) {
			continue;
		}
		const struct filter_rule *action = *action_ptr;

		struct filter_port_range *port_ranges;
		uint32_t port_range_count;
		get_port_range(action, &port_ranges, &port_range_count);
		for (struct filter_port_range *ports = port_ranges;
		     ports < port_ranges + port_range_count;
		     ++ports) {
			if (ports->to - ports->from == 65535) {
				continue;
			}
			for (uint32_t port = ports->from; port <= ports->to;
			     ++port) {
				uint32_t *value = vline_get_ptr(line, port);
				if (remap_table_touch(
					    &remap_table, *value, value
				    ) < 0) {
					goto error_touch;
				}
			}
		}
	}

	remap_table_compact(&remap_table);
	vline_compact(line, &remap_table);
	remap_table_free(&remap_table);

	for (const struct filter_rule **action_ptr = actions;
	     action_ptr < actions + count;
	     ++action_ptr) {
		// A value range should be created even for empty rules
		if (value_registry_start(registry)) {
			goto error_collect;
		}
		if (*action_ptr == NULL) {
			continue;
		}
		const struct filter_rule *action = *action_ptr;

		struct filter_port_range *port_ranges;
		uint32_t port_range_count;
		get_port_range(action, &port_ranges, &port_range_count);
		for (struct filter_port_range *ports = port_ranges;
		     ports < port_ranges + port_range_count;
		     ++ports) {
			for (uint32_t port = ports->from; port <= ports->to;
			     ++port) {
				if (value_registry_collect(
					    registry, vline_get(line, port)
				    )) {
					goto error_collect;
				}
			}
		}

		// Handle default - the full range
		if (!port_range_count) {
			for (uint32_t port = 0; port <= 65535; ++port) {
				if (value_registry_collect(
					    registry, vline_get(line, port)
				    )) {
					goto error_collect;
				}
			}
		}
	}

	return 0;

error_touch:
	remap_table_free(&remap_table);

error_collect:
error_remap_table:
	vline_free(line);
	return -1;
}

static void
get_port_range_src(
	const struct filter_rule *action,
	struct filter_port_range **ranges,
	uint32_t *count
) {
	*ranges = action->transport.srcs;
	*count = action->transport.src_count;
}

static void
get_port_range_dst(
	const struct filter_rule *action,
	struct filter_port_range **ranges,
	uint32_t *count
) {
	*ranges = action->transport.dsts;
	*count = action->transport.dst_count;
}

int
FILTER_ATTR_COMPILER_INIT_FUNC(port_dst)(
	struct value_registry *registry,
	void **data,
	const struct filter_rule **actions,
	size_t actions_count,
	struct memory_context *memory_context
) {
	struct vline *line =
		memory_balloc(memory_context, sizeof(struct vline));
	if (line == NULL) {
		return -1;
	}
	SET_OFFSET_OF(data, line);
	if (collect_port_values(
		    memory_context,
		    actions,
		    actions_count,
		    get_port_range_dst,
		    line,
		    registry
	    )) {
		SET_OFFSET_OF(data, NULL);
		memory_bfree(memory_context, line, sizeof(struct vline));
		return -1;
	}
	return 0;
}

int
FILTER_ATTR_COMPILER_INIT_FUNC(port_src)(
	struct value_registry *registry,
	void **data,
	const struct filter_rule **actions,
	size_t actions_count,
	struct memory_context *memory_context
) {
	struct vline *line =
		memory_balloc(memory_context, sizeof(struct vline));
	if (line == NULL) {
		return -1;
	}
	SET_OFFSET_OF(data, line);
	if (collect_port_values(
		    memory_context,
		    actions,
		    actions_count,
		    get_port_range_src,
		    line,
		    registry
	    )) {
		SET_OFFSET_OF(data, NULL);
		memory_bfree(memory_context, line, sizeof(struct vline));
		return -1;
	}

	return 0;
}

void
FILTER_ATTR_COMPILER_FREE_FUNC(port_src)(
	void *data, struct memory_context *memory_context
) {
	struct vline *line = (struct vline *)data;
	if (line == NULL) {
		return;
	}

	vline_free(line);
	memory_bfree(memory_context, line, sizeof(struct vline));
}

void
FILTER_ATTR_COMPILER_FREE_FUNC(port_dst)(
	void *data, struct memory_context *memory_context
) {
	struct vline *line = (struct vline *)data;
	if (line == NULL) {
		return;
	}

	vline_free(line);
	memory_bfree(memory_context, line, sizeof(struct vline));
}
