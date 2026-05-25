import type { AgentInfo, InstanceInfo } from '../../../api/inspect';

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

export type AgentKind = 'module' | 'system' | 'meta';

/** Aggregated memory and generation metrics for one named agent. */
export interface AgentUsage {
    name: string;
    kind: AgentKind;
    used: number;
    limit: number;
    free: number;
    pct: number;
    gen: number;
    instances: number;
}

/** Instance-level memory totals derived from all agent usages. */
export interface MemoryTotals {
    memUsed: number;
    memLimit: number;
    agents: number;
    agentsActive: number;
    hot: AgentUsage | null;
}

const META_AGENTS = new Set(['function', 'pipeline']);

/**
 * Classify and aggregate per-agent memory metrics from instance.agents.
 *
 * Classification priority: meta (function/pipeline) > module (dp_module name
 * match) > system (everything else — plain, vlan, and any future device-type
 * agents regardless of whether devices of that type are present).
 */
export const computeAgentUsage = (instance: InstanceInfo): Map<string, AgentUsage> => {
    const moduleNames = new Set((instance.dp_modules ?? []).map((m) => m.name ?? ''));

    const result = new Map<string, AgentUsage>();
    for (const agent of (instance.agents ?? []) as AgentInfo[]) {
        const name = agent.name ?? '';
        let kind: AgentKind;
        if (META_AGENTS.has(name)) {
            kind = 'meta';
        } else if (moduleNames.has(name)) {
            kind = 'module';
        } else {
            kind = 'system';
        }

        let limit = 0;
        let free = 0;
        let maxGen = 0;
        const instanceList = agent.instances ?? [];
        for (const inst of instanceList) {
            limit += Number(inst.memory_limit ?? 0);
            free += Number(inst.free_bytes ?? 0);
            const g = Number(inst.generation ?? 0);
            if (g > maxGen) maxGen = g;
        }
        const used = Math.max(0, limit - free);
        const pct = limit > 0 ? used / limit : 0;

        result.set(name, {
            name,
            kind,
            used,
            limit,
            free,
            pct,
            gen: maxGen,
            instances: instanceList.length,
        });
    }
    return result;
};

/**
 * Compute instance-level memory totals from a pre-built agent usage map.
 */
export const computeMemoryTotals = (usage: Map<string, AgentUsage>): MemoryTotals => {
    let memUsed = 0;
    let memLimit = 0;
    let agents = 0;
    let agentsActive = 0;
    let hot: AgentUsage | null = null;

    for (const u of usage.values()) {
        memUsed += u.used;
        memLimit += u.limit;
        agents += 1;
        if (u.used > 0) {
            agentsActive += 1;
            if (!hot || u.pct > hot.pct) {
                hot = u;
            }
        }
    }
    return { memUsed, memLimit, agents, agentsActive, hot };
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
