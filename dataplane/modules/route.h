#ifndef ROUTE_H
#define ROUTE_H

#include "pipeline.h"
#include "module.h"

struct route {

};

struct route_module {
	struct module module;

	struct filter filter;

};

struct route_module *
new_route();

#endif
