#include "cp_counter.h"

#include "controlplane/config/zone.h"

int
cp_pipeline_module_counter_storage_registry_init(
	struct memory_context *memory_context,
	struct cp_pipeline_module_counter_storage_registry *new_registry
) {
	if (registry_init(memory_context, &new_registry->registry, 8)) {
		return -1;
	}

	SET_OFFSET_OF(&new_registry->memory_context, memory_context);
	return 0;
}

struct cp_pipeline_module_counter_storage {
	struct registry_item item;
	char pipeline_name[CP_PIPELINE_NAME_LEN];
	uint64_t module_type;
	char module_name[CP_MODULE_NAME_LEN];
	struct counter_storage *storage;
};

struct cp_pipeline_module_cmp_data {
	const char *pipeline_name;
	uint64_t module_type;
	const char *module_name;
};

static int
cp_pipeline_module_counter_storage_item_cmp(
	const struct registry_item *item, const void *data
) {
	const struct cp_pipeline_module_counter_storage *storage = container_of(
		item, struct cp_pipeline_module_counter_storage, item
	);

	const struct cp_pipeline_module_cmp_data *cmp_data =
		(const struct cp_pipeline_module_cmp_data *)data;

	int res =
		strncmp(storage->pipeline_name,
			cmp_data->pipeline_name,
			CP_PIPELINE_NAME_LEN);
	if (res)
		return res;
	res = storage->module_type - cmp_data->module_type;
	if (res)
		return res;
	return strncmp(
		storage->module_name, cmp_data->module_name, CP_MODULE_NAME_LEN
	);
}

struct counter_storage *
cp_pipeline_module_counter_storage_registry_lookup(
	struct cp_pipeline_module_counter_storage_registry *registry,
	const char *pipeline_name,
	uint64_t module_type,
	const char *module_name
) {
	struct cp_pipeline_module_cmp_data cmp_data = {
		.pipeline_name = pipeline_name,
		.module_type = module_type,
		.module_name = module_name,
	};

	uint64_t index;
	if (registry_lookup(
		    &registry->registry,
		    cp_pipeline_module_counter_storage_item_cmp,
		    &cmp_data,
		    &index
	    )) {
		return NULL;
	}

	struct cp_pipeline_module_counter_storage *item = container_of(
		registry_get(&registry->registry, index),
		struct cp_pipeline_module_counter_storage,
		item
	);

	return ADDR_OF(&item->storage);
}

static void
cp_pipeline_module_couter_storage_item_free_cb(
	struct registry_item *item, void *data
) {
	struct cp_pipeline_module_counter_storage *storage = container_of(
		item, struct cp_pipeline_module_counter_storage, item
	);
	struct memory_context *memory_context = (struct memory_context *)data;

	counter_storage_free(ADDR_OF(&storage->storage));

	memory_bfree(
		memory_context,
		storage,
		sizeof(struct cp_pipeline_module_counter_storage)
	);
}

int
cp_pipeline_module_counter_storage_registry_insert(
	struct cp_pipeline_module_counter_storage_registry *registry,
	char *pipeline_name,
	uint64_t module_type,
	char *module_name,
	struct counter_storage *counter_storage
) {
	struct cp_pipeline_module_counter_storage *cs =
		(struct cp_pipeline_module_counter_storage *)memory_balloc(
			ADDR_OF(&registry->memory_context),
			sizeof(struct cp_pipeline_module_counter_storage)
		);

	strtcpy(cs->pipeline_name, pipeline_name, CP_PIPELINE_NAME_LEN);
	cs->module_type = module_type;
	strtcpy(cs->module_name, module_name, CP_MODULE_NAME_LEN);
	SET_OFFSET_OF(&cs->storage, counter_storage);

	struct cp_pipeline_module_cmp_data cmp_data = {
		.pipeline_name = pipeline_name,
		.module_type = module_type,
		.module_name = module_name,
	};

	return registry_replace(
		&registry->registry,
		cp_pipeline_module_counter_storage_item_cmp,
		&cmp_data,
		&cs->item,
		cp_pipeline_module_couter_storage_item_free_cb,
		ADDR_OF(&registry->memory_context)
	);
}

void
cp_pipeline_module_counter_storage_registry_destroy(
	struct cp_pipeline_module_counter_storage_registry
		*counter_storage_registry
) {
	struct memory_context *memory_context =
		ADDR_OF(&counter_storage_registry->memory_context);
	registry_destroy(
		&counter_storage_registry->registry,
		cp_pipeline_module_couter_storage_item_free_cb,
		memory_context
	);
}

int
cp_pipeline_counter_storage_registry_init(
	struct memory_context *memory_context,
	struct cp_pipeline_counter_storage_registry *new_registry
) {
	if (registry_init(memory_context, &new_registry->registry, 8)) {
		return -1;
	}

	SET_OFFSET_OF(&new_registry->memory_context, memory_context);
	return 0;
}

struct cp_pipeline_counter_storage {
	struct registry_item item;
	char pipeline_name[CP_PIPELINE_NAME_LEN];
	struct counter_storage *storage;
};

struct cp_pipeline_cmp_data {
	const char *pipeline_name;
};

static int
cp_pipeline_counter_storage_item_cmp(
	const struct registry_item *item, const void *data
) {
	const struct cp_pipeline_counter_storage *storage =
		container_of(item, struct cp_pipeline_counter_storage, item);

	const struct cp_pipeline_cmp_data *cmp_data =
		(const struct cp_pipeline_cmp_data *)data;

	return strncmp(
		storage->pipeline_name,
		cmp_data->pipeline_name,
		CP_PIPELINE_NAME_LEN
	);
}

struct counter_storage *
cp_pipeline_counter_storage_registry_lookup(
	struct cp_pipeline_counter_storage_registry *registry,
	const char *pipeline_name
) {
	struct cp_pipeline_cmp_data cmp_data = {
		.pipeline_name = pipeline_name,
	};

	uint64_t index;
	if (registry_lookup(
		    &registry->registry,
		    cp_pipeline_counter_storage_item_cmp,
		    &cmp_data,
		    &index
	    )) {
		return NULL;
	}

	return ADDR_OF(&container_of(
				registry_get(&registry->registry, index),
				struct cp_pipeline_counter_storage,
				item
	)
				->storage);
}

static void
cp_pipeline_couter_storage_item_free_cb(
	struct registry_item *item, void *data
) {
	struct cp_pipeline_counter_storage *storage =
		container_of(item, struct cp_pipeline_counter_storage, item);
	struct memory_context *memory_context = (struct memory_context *)data;

	counter_storage_free(ADDR_OF(&storage->storage));

	memory_bfree(
		memory_context,
		storage,
		sizeof(struct cp_pipeline_counter_storage)
	);
}

int
cp_pipeline_counter_storage_registry_insert(
	struct cp_pipeline_counter_storage_registry *registry,
	char *pipeline_name,
	struct counter_storage *counter_storage
) {
	struct cp_pipeline_counter_storage *cs =
		(struct cp_pipeline_counter_storage *)memory_balloc(
			ADDR_OF(&registry->memory_context),
			sizeof(struct cp_pipeline_counter_storage)
		);

	strtcpy(cs->pipeline_name, pipeline_name, CP_PIPELINE_NAME_LEN);
	SET_OFFSET_OF(&cs->storage, counter_storage);

	struct cp_pipeline_cmp_data cmp_data = {
		.pipeline_name = pipeline_name,
	};

	return registry_replace(
		&registry->registry,
		cp_pipeline_counter_storage_item_cmp,
		&cmp_data,
		&cs->item,
		cp_pipeline_couter_storage_item_free_cb,
		ADDR_OF(&registry->memory_context)
	);
}

void
cp_pipeline_counter_storage_registry_destroy(
	struct cp_pipeline_counter_storage_registry *counter_storage_registry
) {
	struct memory_context *memory_context =
		ADDR_OF(&counter_storage_registry->memory_context);
	registry_destroy(
		&counter_storage_registry->registry,
		cp_pipeline_couter_storage_item_free_cb,
		memory_context
	);
}
