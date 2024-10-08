#include "module.h"

#include <string.h>

struct module_list;

struct module_list {
	struct module_list *next;
	struct module *module;
};

static struct module_list *module_list;

struct module *
module_lookup(const char *name)
{
	for (struct module_list *module_item = module_list;
	     module_item != NULL;
	     module_item = module_item->next) {
		if (!strncmp(module_item->module->name, name, MODULE_NAME_LEN)) {
			return module_item->module;
		}
	}
	return NULL;
}

int
module_register(struct module *module)
{
	struct module_list *module_item =
		(struct module_list *)malloc(sizeof(struct module_list));
	if (module_item == NULL) {
		return -1;
	}
	module_item->module = module;
	module_item->next = module_list;
	module_list = module_item;

	return 0;
}
