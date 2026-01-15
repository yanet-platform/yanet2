#include "config.h"

#include <rte_ether.h>
#include <rte_ip.h>

#include "common/container_of.h"

#include "dataplane/module/module.h"
#include "dataplane/packet/packet.h"
#include "dataplane/pipeline/pipeline.h"

#include "stdio.h"

static void
proxy_handle_packets(
    struct dp_worker *dp_worker,
    struct module_ectx *module_ectx,
    struct packet_front *packet_front 
) {
    (void)dp_worker;
    (void)module_ectx;
    // struct proxy_module_config *module_config = container_of(
    //     ADDR_OF(&module_ectx->cp_module),
    //     struct proxy_module_config,
    //     cp_module
    // );

    struct packet *packet;
    while ((packet = packet_list_pop(&packet_front->input)) != NULL) {
        packet_front_output(packet_front, packet);
    }
}

struct proxy_module {
    struct module module;
};

struct module *
new_module_proxy() {
    struct proxy_module *module = 
        (struct proxy_module *)malloc(sizeof(struct proxy_module));
    if (module == NULL) {
        return NULL;
    }

    snprintf(
        module->module.name,
        sizeof(module->module.name),
        "%s",
        "proxy"
    );
    module->module.handler = proxy_handle_packets;

    return &module->module;
}