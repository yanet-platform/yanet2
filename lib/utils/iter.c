#include "iter.h"

#include "api/info.h"

void
dp_config_iterate_module_configs(
    struct dp_config *dp_config, 
    const char *device, 
    const char *pipeline, 
    const char *function, 
    const char *chain,
    const char *module_type,
    const char *config,
    void *userdata,
    dp_config_iterate_module_configs_callback
) {
    struct cp_device_list_info *devices = yanet_get_cp_device_list_info(dp_config);
    // traverse devices
    for (size_t device_idx = 0; device_idx < devices->device_count; ++device_idx) {
        struct cp_device_info *device_info = &devices->devices[device_idx];
        if (device != NULL && strcmp(device, device_info->name) != 0) {
            // skip devices
            continue;
        }

        // traverse pipelines
        for (size_t pipeline_idx = 0; pipeline_idx < device_info->pipeline_count; ++pipeline_idx) {
            struct cp_pipeline_info *pipeline_info = &device_info->pipelines[pipeline_idx];
            if (pipeline != NULL && strcmp(pipeline, pipeline_info->name) != 0) {
                // skip pipeline
                continue;
            }

            // todo: traverse over functions
        }
    }
    cp_device_list_info_free(devices);
}