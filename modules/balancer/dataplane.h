#pragma once

#include "dataplane/module/module.h"

#define VS_OPT_ENCAP 0x01
#define VS_OPT_GRE 0x02

#define RS_TYPE_V4 0x01
#define RS_TYPE_V6 0x02

struct module *
new_module_balancer();
