#include "cp_device.h"

#include "common/container_of.h"

#include "controlplane/agent/agent.h"
#include "dataplane/config/zone.h"

#include "controlplane/config/zone.h"

int
cp_device_config_init(
	struct cp_device_config *cp_device_config,
	const char *type,
	const char *name,
	uint64_t input_pipeline_count,
	uint64_t output_pipeline_count,
	yanet_error **err
) {
	memset(cp_device_config, 0, sizeof(struct cp_device_config));
	strtcpy(cp_device_config->type, type, sizeof(cp_device_config->type));
	strtcpy(cp_device_config->name, name, CP_DEVICE_NAME_LEN);
	cp_device_config->input_pipelines = (struct cp_device_entry_config *)
		malloc(sizeof(struct cp_device_entry_config) +
		       sizeof(struct cp_pipeline_weight_config) *
			       input_pipeline_count);
	if (cp_device_config->input_pipelines == NULL) {
		yanet_error_add(
			err,
			"failed to allocate memory for input pipelines of "
			"device '%s'",
			name
		);
		goto error;
	}
	memset(cp_device_config->input_pipelines,
	       0,
	       sizeof(struct cp_device_entry_config) +
		       sizeof(struct cp_pipeline_weight_config) *
			       input_pipeline_count);
	cp_device_config->input_pipelines->count = input_pipeline_count;

	cp_device_config->output_pipelines = (struct cp_device_entry_config *)
		malloc(sizeof(struct cp_device_entry_config) +
		       sizeof(struct cp_pipeline_weight_config) *
			       output_pipeline_count);
	if (cp_device_config->output_pipelines == NULL) {
		yanet_error_add(
			err,
			"failed to allocate memory for output pipelines of "
			"device '%s'",
			name
		);
		goto error_output;
	}
	memset(cp_device_config->output_pipelines,
	       0,
	       sizeof(struct cp_device_entry_config) +
		       sizeof(struct cp_pipeline_weight_config) *
			       output_pipeline_count);
	cp_device_config->output_pipelines->count = output_pipeline_count;

	return 0;

error_output:
	free(cp_device_config->input_pipelines);

error:
	return -1;
}

void
cp_device_config_fini(struct cp_device_config *config) {
	free(config->output_pipelines);
	free(config->input_pipelines);
	config->output_pipelines = NULL;
	config->input_pipelines = NULL;
}

static inline uint64_t
cp_device_entry_alloc_size(uint64_t pipeline_count) {
	return sizeof(struct cp_device_entry) +
	       sizeof(struct cp_device_pipeline) * pipeline_count;
}

static struct cp_device_entry *
cp_device_entry_create(
	struct memory_context *memory_context,
	struct cp_device_entry_config *cp_device_entry_config,
	yanet_error **err
) {
	uint64_t alloc_size =
		cp_device_entry_alloc_size(cp_device_entry_config->count);
	struct cp_device_entry *cp_device_entry = (struct cp_device_entry *)
		memory_balloc(memory_context, alloc_size);
	if (cp_device_entry == NULL) {
		yanet_error_add(
			err, "failed to allocate memory for device entry"
		);
		return NULL;
	}
	memset(cp_device_entry, 0, alloc_size);
	cp_device_entry->pipeline_count = cp_device_entry_config->count;
	for (uint64_t idx = 0; idx < cp_device_entry_config->count; ++idx) {
		struct cp_device_pipeline *cp_device_pipeline =
			cp_device_entry->pipelines + idx;
		struct cp_pipeline_weight_config *cp_device_pipeline_config =
			cp_device_entry_config->pipelines + idx;
		strtcpy(cp_device_pipeline->name,
			cp_device_pipeline_config->name,
			CP_PIPELINE_NAME_LEN);
		cp_device_pipeline->weight = cp_device_pipeline_config->weight;
	}

	return cp_device_entry;
}

struct cp_device *
cp_device_new(struct memory_context *mctx) {
	struct cp_device *self = (struct cp_device *)memory_balloc(
		mctx, sizeof(struct cp_device)
	);
	if (self == NULL) {
		return NULL;
	}

	memset(self, 0, sizeof(struct cp_device));
	SET_OFFSET_OF(&self->parent_memory_context, mctx);
	self->alloc_size = sizeof(struct cp_device);

	return self;
}

void
cp_device_free(struct cp_device *self) {
	if (self == NULL) {
		return;
	}

	struct memory_context *mctx = ADDR_OF(&self->parent_memory_context);
	memory_bfree(mctx, self, self->alloc_size);
}

int
cp_device_init(
	struct cp_device *self,
	struct agent *agent,
	const struct cp_device_config *cfg,
	yanet_error **err
) {
	struct dp_config *dp_config = ADDR_OF(&agent->dp_config);

	if (dp_config_lookup_device(
		    dp_config, cfg->type, &self->dp_device_idx
	    )) {
		yanet_error_add(
			err,
			"device type '%s' not found in dataplane config",
			cfg->type
		);
		goto err_out;
	}
	strtcpy(self->type, cfg->type, sizeof(self->type));
	strtcpy(self->name, cfg->name, sizeof(self->name));
	memory_context_init_from(
		&self->memory_context, &agent->memory_context, cfg->name
	);

	SET_OFFSET_OF(&self->agent, agent);

	struct memory_context *memory_context = &self->memory_context;

	SET_OFFSET_OF(
		&self->input_pipelines,
		cp_device_entry_create(
			memory_context, cfg->input_pipelines, err
		)
	);
	if (self->input_pipelines == NULL) {
		yanet_error_add(
			err,
			"failed to create input pipelines for device '%s'",
			cfg->name
		);
		goto err_out;
	}

	SET_OFFSET_OF(
		&self->output_pipelines,
		cp_device_entry_create(
			memory_context, cfg->output_pipelines, err
		)
	);
	if (self->output_pipelines == NULL) {
		yanet_error_add(
			err,
			"failed to create output pipelines for device '%s'",
			cfg->name
		);
		goto err_out;
	}

	registry_item_init(&self->config_item);

	if (counter_registry_init(&self->counter_registry, memory_context, 0)) {
		yanet_error_add(
			err,
			"failed to initialize counter registry for device '%s'",
			cfg->name
		);
		goto err_out;
	}

	self->counter_packet_rx_count = counter_registry_register(
		&self->counter_registry, "rx", 1, err
	);
	if (self->counter_packet_rx_count == COUNTER_INVALID) {
		yanet_error_add(
			err,
			"failed to register 'rx' counter for device '%s'",
			cfg->name
		);
		goto err_out;
	}

	self->counter_packet_tx_count = counter_registry_register(
		&self->counter_registry, "tx", 1, err
	);
	if (self->counter_packet_tx_count == COUNTER_INVALID) {
		yanet_error_add(
			err,
			"failed to register 'tx' counter for device '%s'",
			cfg->name
		);
		goto err_out;
	}

	self->counter_packet_rx_bytes = counter_registry_register(
		&self->counter_registry, "rx_bytes", 1, err
	);
	if (self->counter_packet_rx_bytes == COUNTER_INVALID) {
		yanet_error_add(
			err,
			"failed to register 'rx_bytes' counter for device '%s'",
			cfg->name
		);
		goto err_out;
	}

	self->counter_packet_tx_bytes = counter_registry_register(
		&self->counter_registry, "tx_bytes", 1, err
	);
	if (self->counter_packet_tx_bytes == COUNTER_INVALID) {
		yanet_error_add(
			err,
			"failed to register 'tx_bytes' counter for device '%s'",
			cfg->name
		);
		goto err_out;
	}

	return 0;

err_out:
	cp_device_fini(self);
	return -1;
}

static void
cp_device_entry_free(
	struct memory_context *memory_context,
	struct cp_device_entry *cp_device_entry
) {
	if (cp_device_entry == NULL)
		return;
	memory_bfree(
		memory_context,
		cp_device_entry,
		cp_device_entry_alloc_size(cp_device_entry->pipeline_count)
	);
}

void
cp_device_fini(struct cp_device *self) {
	struct memory_context *memory_context = &self->memory_context;

	// Release the device's counter table.
	counter_registry_fini(&self->counter_registry);

	cp_device_entry_free(memory_context, ADDR_OF(&self->output_pipelines));
	SET_OFFSET_OF(&self->output_pipelines, NULL);

	cp_device_entry_free(memory_context, ADDR_OF(&self->input_pipelines));
	SET_OFFSET_OF(&self->input_pipelines, NULL);

	SET_OFFSET_OF(&self->agent, NULL);
}

int
cp_device_registry_init(
	struct memory_context *memory_context,
	struct cp_device_registry *new_device_registry,
	yanet_error **err
) {
	if (registry_init(memory_context, &new_device_registry->registry, 8)) {
		yanet_error_add(err, "failed to initialize device registry");
		return -1;
	}

	SET_OFFSET_OF(&new_device_registry->memory_context, memory_context);
	return 0;
}

int
cp_device_registry_copy(
	struct memory_context *memory_context,
	struct cp_device_registry *new_device_registry,
	struct cp_device_registry *old_device_registry,
	yanet_error **err
) {
	if (registry_copy(
		    memory_context,
		    &new_device_registry->registry,
		    &old_device_registry->registry
	    )) {
		yanet_error_add(err, "failed to copy device registry");
		return -1;
	};

	SET_OFFSET_OF(&new_device_registry->memory_context, memory_context);
	return 0;
}

static void
cp_device_registry_item_free_cb(struct registry_item *item, void *data) {
	(void)data;
	struct cp_device *device =
		container_of(item, struct cp_device, config_item);
	cp_device_fini(device);
	cp_device_free(device);
}

void
cp_device_registry_destroy(struct cp_device_registry *device_registry) {
	registry_destroy(
		&device_registry->registry,
		cp_device_registry_item_free_cb,
		NULL
	);
}

struct cp_device *
cp_device_registry_get(
	struct cp_device_registry *device_registry, uint64_t index
) {
	struct registry_item *item =
		registry_get(&device_registry->registry, index);
	if (item == NULL) {
		return NULL;
	}
	return container_of(item, struct cp_device, config_item);
}

static int
cp_device_registry_item_cmp(
	const struct registry_item *item, const void *data
) {
	const struct cp_device *device =
		container_of(item, struct cp_device, config_item);

	return strncmp(device->name, (const char *)data, CP_DEVICE_NAME_LEN);
}

struct cp_device *
cp_device_registry_lookup(
	struct cp_device_registry *device_registry, const char *name
) {
	uint64_t index;
	if (registry_lookup(
		    &device_registry->registry,
		    cp_device_registry_item_cmp,
		    name,
		    &index
	    )) {
		return NULL;
	}

	return container_of(
		registry_get(&device_registry->registry, index),
		struct cp_device,
		config_item
	);
}

int
cp_device_registry_upsert(
	struct cp_device_registry *device_registry,
	const char *name,
	struct cp_device *new_device,
	yanet_error **err
) {
	struct cp_device *old_device =
		cp_device_registry_lookup(device_registry, name);

	if (counter_registry_link(
		    &new_device->counter_registry,
		    (old_device != NULL) ? &old_device->counter_registry : NULL,
		    err
	    )) {
		yanet_error_add(
			err,
			"failed to link counter registry for device '%s'",
			name
		);
		return -1;
	}

	if (registry_replace(
		    &device_registry->registry,
		    cp_device_registry_item_cmp,
		    name,
		    &new_device->config_item,
		    cp_device_registry_item_free_cb,
		    NULL
	    )) {
		yanet_error_add(err, "failed to replace device in registry");
		return -1;
	}

	return 0;
}

int
cp_device_registry_delete(
	struct cp_device_registry *device_registry, const char *name
) {
	return registry_replace(
		&device_registry->registry,
		cp_device_registry_item_cmp,
		name,
		NULL,
		cp_device_registry_item_free_cb,
		NULL
	);
}
