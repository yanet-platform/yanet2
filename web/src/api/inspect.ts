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
    Name?: string;
    Weight?: string | number; // uint64 - serialized as string in JSON
    modules?: ChainModuleInfo[];
}

export interface FunctionInfo {
    Name?: string;
    chains?: FunctionChainInfo[];
}

export interface PipelineInfo {
    name?: string;
    functions?: string[];
}

export interface AgentInstanceInfo {
    pid?: number;
    memoryLimit?: string | number; // uint64 - serialized as string in JSON
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
    inputPipelines?: DevicePipelineInfo[]; // input_pipelines
    outputPipelines?: DevicePipelineInfo[]; // output_pipelines
}

export interface InstanceInfo {
    instanceIdx?: number; // instance_idx
    numaIdx?: number; // numa_idx
    dpModules?: DPModuleInfo[]; // dp_modules
    cpConfigs?: CPConfigInfo[]; // cp_configs
    functions?: FunctionInfo[];
    pipelines?: PipelineInfo[];
    agents?: AgentInfo[];
    devices?: DeviceInfo[];
}

export interface InspectResponse {
    instanceInfo?: InstanceInfo;
}

const inspectService = createService('ynpb.InspectService');

export const inspect = {
    inspect: (options?: CallOptions): Promise<InspectResponse> => {
        return inspectService.call<InspectResponse>('Inspect', options);
    },
};
