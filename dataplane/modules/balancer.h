#ifndef BALANCER_H
#define BALANCER_H

#include "module.h"

#include "filter.h"

struct balancer_vs {
	uint32_t options;

};

struct balancer_rs {

};

struct balancer_module {
	struct module module;

	struct filter filter;

	uint32_t vs_count;
};

struct balancer_module *
new_balancer();


#endif
