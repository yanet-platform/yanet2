import type { InstanceInfo } from '../../../api/inspect';

/** Per-module deep-link route. Modules without a dedicated page are absent. */
export const MODULE_ROUTES: Record<string, string> = {
    forward: '/modules/forward',
    route:   '/modules/route',
    decap:   '/modules/decap',
    acl:     '/modules/acl',
    pdump:   '/modules/pdump',
};

/** Human-readable description for each known dataplane module. */
export const MODULE_DESCRIPTIONS: Record<string, string> = {
    forward: 'Packet forwarding',
    route: 'Routing module',
    decap: 'Packet decapsulation',
    dscp: 'DSCP marking',
    nat64: 'NAT64 translation',
    acl: 'Access control list',
    pdump: 'Packet dump',
    fwstate: 'Stateful firewall',
    'route-mpls': 'MPLS routing',
    balancer2: 'Load balancer',
};

/**
 * Count how many dataplane modules are actively used by at least one
 * config or pipeline function chain.
 */
export const computeModulesInUse = (instance: InstanceInfo): number => {
    const configs = instance.cp_configs ?? [];
    const pipelines = instance.pipelines ?? [];
    const functions = instance.functions ?? [];
    const modules = instance.dp_modules ?? [];

    const used = new Set<string>();
    for (const cfg of configs) {
        const t = cfg.type?.toLowerCase();
        if (t) used.add(t);
    }
    const funcByName = new Map(functions.map((f) => [f.name ?? '', f]));
    for (const pipe of pipelines) {
        for (const fname of pipe.functions ?? []) {
            const fn = funcByName.get(fname);
            for (const ch of fn?.chains ?? []) {
                for (const m of ch.modules ?? []) {
                    const t = m.type?.toLowerCase();
                    if (t) used.add(t);
                }
            }
        }
    }
    let count = 0;
    for (const m of modules) {
        const t = m.name?.toLowerCase() ?? '';
        if (used.has(t)) count += 1;
    }
    return count;
};

/**
 * Return a map of module name -> number of pipelines that use that module
 * (via any function chain).
 */
export const computeModulePipelineUsage = (instance: InstanceInfo): Map<string, number> => {
    const pipelines = instance.pipelines ?? [];
    const functions = instance.functions ?? [];

    const result = new Map<string, number>();
    const funcByName = new Map(functions.map((f) => [f.name ?? '', f]));

    for (const pipe of pipelines) {
        const seen = new Set<string>();
        for (const fname of pipe.functions ?? []) {
            const fn = funcByName.get(fname);
            for (const ch of fn?.chains ?? []) {
                for (const m of ch.modules ?? []) {
                    const t = m.type?.toLowerCase();
                    if (t && !seen.has(t)) {
                        seen.add(t);
                        result.set(t, (result.get(t) ?? 0) + 1);
                    }
                }
            }
        }
    }
    return result;
};
