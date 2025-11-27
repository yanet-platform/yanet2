#include <stdlib.h>

struct module;
struct device;

void *
keep_refs() {
	extern struct module *new_module_balancer(void);
	extern struct module *new_module_decap(void);
	extern struct module *new_module_dscp(void);
	extern struct module *new_module_acl(void);
	extern struct module *new_module_forward(void);
	extern struct module *new_module_route(void);
	extern struct module *new_module_nat64(void);
	extern struct module *new_module_pdump(void);

	extern struct device *new_device_plain(void);
	extern struct device *new_device_vlan(void);

	void *p = NULL;
	p = (void *)new_module_balancer;
	p = (void *)new_module_decap;
	p = (void *)new_module_dscp;
	p = (void *)new_module_acl;
	p = (void *)new_module_forward;
	p = (void *)new_module_route;
	p = (void *)new_module_nat64;
	p = (void *)new_module_pdump;

	p = (void *)new_device_plain;
	p = (void *)new_device_vlan;

	return p;
}