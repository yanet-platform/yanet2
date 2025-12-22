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

// Device types enum
export const DEVICE_TYPES = ['plain', 'vlan'] as const;
export type DeviceType = typeof DEVICE_TYPES[number];

const deviceService = createService('ynpb.DeviceService');
const plainService = createService('plainpb.DevicePlainService');
const vlanService = createService('vlanpb.DeviceVlanService');

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
