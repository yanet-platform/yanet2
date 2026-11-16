#pragma once

#define CP_MODULE_NAME_LEN 80
#define CP_MODULE_TYPE_LEN 80
#define CP_CHAIN_NAME_LEN 80
#define CP_FUNCTION_NAME_LEN 80
#define CP_PIPELINE_NAME_LEN 80
#define CP_DEVICE_NAME_LEN 80
#define CP_DEVICE_TYPE_LEN 80
#define CP_OBJECT_NAME_LEN 80
#define CP_OBJECT_TYPE_LEN 80

// Maximum total weight of one chain or pipeline selection table.
//
// A weighted selection table holds one pointer per weight unit, so the
// bound caps each table at about 512 KiB per worker and occurrence in the
// execution graph.
#define YANET_MAX_WEIGHT_SUM 65535
