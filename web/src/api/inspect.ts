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
    memory_limit?: string | number; // uint64 - serialized as string in JSON
    allocated?: string | number; // uint64 - serialized as string in JSON
    freed?: string | number; // uint64 - serialized as string in JSON
    generation?: string | number; // uint64 - serialized as string in JSON
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

const inspectService = createService('ynpb.InspectService');

export const inspect = {
    inspect: (options?: CallOptions): Promise<InspectResponse> => {
        return inspectService.call<InspectResponse>('Inspect', options);
    },
};
