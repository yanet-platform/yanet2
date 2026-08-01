import { createService, type CallOptions } from './client';

// Inspect types
export interface DPModuleInfo {
    name: string;
}

export interface CPConfigInfo {
    type?: string;
    name?: string;
    generation?: string | number; // uint64 - serialized as string in JSON
}

export interface ChainModuleInfo {
    type?: string;
    name?: string;
}

export interface FunctionChainInfo {
    name?: string;
    weight?: string | number; // uint64 - serialized as string in JSON
    modules?: ChainModuleInfo[];
}

export interface FunctionInfo {
    name?: string;
    chains?: FunctionChainInfo[];
}

export interface PipelineInfo {
    name?: string;
    functions?: string[];
}

export interface AgentInstanceInfo {
    pid?: number;
    memory_limit?: string | number; // uint64 — serialized as string in JSON
    free_bytes?: string | number; // uint64 — serialized as string in JSON
    generation?: string | number; // uint64 — serialized as string in JSON
}

export interface AgentInfo {
    name?: string;
    instances?: AgentInstanceInfo[];
}

export interface DevicePipelineInfo {
    name?: string;
    weight?: string | number; // uint64 - serialized as string in JSON
}

export interface DeviceInfo {
    type?: string;
    name?: string;
    input_pipelines?: DevicePipelineInfo[];
    output_pipelines?: DevicePipelineInfo[];
}

export interface InstanceInfo {
    instance_idx?: number;
    numa_idx?: number;
    dp_modules?: DPModuleInfo[];
    cp_configs?: CPConfigInfo[];
    functions?: FunctionInfo[];
    pipelines?: PipelineInfo[];
    agents?: AgentInfo[];
    devices?: DeviceInfo[];
}

export interface InspectResponse {
    instance_info?: InstanceInfo;
}

const inspectService = createService('controlplane.ynpb.v1.InspectService');

export const inspect = {
    inspect: (options?: CallOptions): Promise<InspectResponse> => {
        return inspectService.call<InspectResponse>('Inspect', options);
    },
};

/** Config names of the given module type held in the dataplane's shared-memory inventory.
 *
 * The inventory outlives a module control-plane restart, so it is the only source
 * that still knows a config the service's in-process map lost.
 */
export const inventoryConfigNames = async (moduleType: string): Promise<string[]> => {
    const resp = await inspect.inspect();
    const cpConfigs = resp.instance_info?.cp_configs ?? [];
    return cpConfigs
        .filter((config) => config.type === moduleType)
        .map((config) => config.name ?? '')
        .filter(Boolean);
};

/** Union of a module service's own config names with the shared-memory inventory.
 *
 * Service names come first, in their original order. Inventory-only names are
 * appended in inventory order. Every name appears exactly once, even when
 * `serviceNames` itself contains a duplicate. This ordering guarantees that a
 * config already rendered from the service list never moves or disappears —
 * the inventory can only add names, never reorder or drop existing ones.
 */
export const unionConfigNames = (serviceNames: string[], inventoryNames: string[]): string[] =>
    [...new Set([...serviceNames, ...inventoryNames])];
