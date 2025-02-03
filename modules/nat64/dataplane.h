#pragma once

#include "dataplane/module/module.h"

#include "ipfw.h"
#include "lpm.h"
#include "uthash.h"

struct ip4to6 {
    uint8_t* ip4;             
	uint8_t* ip6;
    UT_hash_handle hh;         /* makes this structure hashable */
};

struct nat64_module_config {
	struct module_config config;

	struct lpm map6to4;
	struct lpm map4to6;

	struct ip4to6 *hash4to6;
	struct ip4to6 *hash6to4;

	struct net6 rndsrc;
	
	// TODO: ToS/QoS drop
};


struct nat64_module {
	struct module module;
};

struct module *
new_module_nat64();