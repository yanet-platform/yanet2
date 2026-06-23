import { createService, type CallOptions } from './client';

// Device types based on device.proto and target.proto

export interface DeviceId {
    type?: string;
    name?: string;
}

export interface DevicePipeline {
    name?: string;
    weight?: string | number; // uint64 - serialized as string in JSON
}

export interface Device {
    input?: DevicePipeline[];
    output?: DevicePipeline[];
}

// List devices request/response
export interface ListDevicesRequest { }

export interface ListDevicesResponse {
    ids?: DeviceId[];
}

// Plain device update request/response
export interface UpdateDevicePlainRequest {
    name?: string;
    device?: Device;
}

export interface UpdateDevicePlainResponse {
    error?: string;
}

// VLAN device update request/response
export interface UpdateDeviceVlanRequest {
    name?: string;
    device?: Device;
    vlan?: number;
}

export interface UpdateDeviceVlanResponse {
    error?: string;
}

/** Device type discriminator; the concrete set is owned by the device registry. */
export type DeviceType = string;

/** Parse a pipeline weight that may arrive as a uint64 string or a number. */
export const parseWeight = (weight: string | number | undefined): number => {
    if (weight === undefined) return 0;
    if (typeof weight === 'number') return weight;
    return parseInt(weight, 10) || 0;
};

/** Build the wire Device payload from a device's input/output pipelines. */
export const toDevicePayload = (
    inputPipelines: DevicePipeline[],
    outputPipelines: DevicePipeline[],
): Device => ({
    input: inputPipelines.map((p) => ({ name: p.name, weight: parseWeight(p.weight) })),
    output: outputPipelines.map((p) => ({ name: p.name, weight: parseWeight(p.weight) })),
});

const deviceService = createService('controlplane.ynpb.v1.DeviceService');
const plainService = createService('devices.plain.controlplane.plainpb.v1.DevicePlainService');
const vlanService = createService('devices.vlan.controlplane.vlanpb.v1.DeviceVlanService');

export const devices = {
    list: (request: ListDevicesRequest, options?: CallOptions): Promise<ListDevicesResponse> => {
        return deviceService.callWithBody<ListDevicesResponse>('List', request, options);
    },
    updatePlain: (request: UpdateDevicePlainRequest, options?: CallOptions): Promise<UpdateDevicePlainResponse> => {
        return plainService.callWithBody<UpdateDevicePlainResponse>('UpdateDevice', request, options);
    },
    updateVlan: (request: UpdateDeviceVlanRequest, options?: CallOptions): Promise<UpdateDeviceVlanResponse> => {
        return vlanService.callWithBody<UpdateDeviceVlanResponse>('UpdateDevice', request, options);
    },
};
