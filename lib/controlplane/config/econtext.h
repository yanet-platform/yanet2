#pragma once

struct cp_config_gen;

struct config_gen_ectx;

struct config_gen_ectx *
config_gen_ectx_create(
	struct cp_config_gen *config_gen, struct cp_config_gen *old_config_gen
);

void
config_gen_ectx_free(
	struct cp_config_gen *config_gen,
	struct config_gen_ectx *config_gen_ectx
);
