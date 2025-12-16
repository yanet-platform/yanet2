import type { DeviceId, DevicePipeline, DeviceType } from '../../api/devices';

export interface LocalDevice {
    id: DeviceId;
    type: DeviceType;
    inputPipelines: DevicePipeline[];
    outputPipelines: DevicePipeline[];
    vlanId?: number; // Only for vlan devices
    isNew: boolean; // True if device was created locally but not saved yet
    isDirty: boolean;
}
