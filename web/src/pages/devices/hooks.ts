import { useState, useCallback, useEffect } from 'react';
import { API } from '../../api';
import type { DeviceType } from '../../api/devices';
import type { PipelineId } from '../../api/pipelines';
import type { InspectResponse, DeviceInfo } from '../../api/inspect';
import { toaster } from '../../utils';
import type { LocalDevice, InstanceDeviceData } from './types';

export interface UseDeviceDataResult {
    instances: InstanceDeviceData[];
    loading: boolean;
    error: string | null;
    reloadInstances: () => Promise<void>;
    createDevice: (instance: number, name: string, type: DeviceType) => void;
    updateDevice: (instance: number, deviceName: string, updates: Partial<LocalDevice>) => void;
    saveDevice: (instance: number, device: LocalDevice) => Promise<boolean>;
    loadPipelineList: (instance: number) => Promise<PipelineId[]>;
}

const parseWeight = (weight: string | number | undefined): number => {
    if (weight === undefined) return 0;
    if (typeof weight === 'number') return weight;
    return parseInt(weight, 10) || 0;
};

const deviceInfoToLocal = (info: DeviceInfo): LocalDevice => {
    const type = (info.type === 'vlan' ? 'vlan' : 'plain') as DeviceType;
    return {
        id: { type: info.type, name: info.name },
        type,
        inputPipelines: (info.inputPipelines || []).map(p => ({
            name: p.name,
            weight: parseWeight(p.weight),
        })),
        outputPipelines: (info.outputPipelines || []).map(p => ({
            name: p.name,
            weight: parseWeight(p.weight),
        })),
        isNew: false,
        isDirty: false,
    };
};

/**
 * Hook for managing device data and API interactions
 */
export const useDeviceData = (): UseDeviceDataResult => {
    const [instances, setInstances] = useState<InstanceDeviceData[]>([]);
    const [loading, setLoading] = useState(true);
    const [error, setError] = useState<string | null>(null);

    const loadInstancesAndDevices = useCallback(async (): Promise<void> => {
        setLoading(true);
        setError(null);

        try {
            // Get all instances and their devices from inspect
            const inspectResponse: InspectResponse = await API.inspect.inspect();
            const instanceInfo = inspectResponse.instanceInfo || [];

            const instanceData: InstanceDeviceData[] = instanceInfo.map((info) => ({
                instance: info.instanceIdx ?? 0,
                devices: (info.devices || []).map(deviceInfoToLocal),
            }));

            setInstances(instanceData);
        } catch (err) {
            const message = err instanceof Error ? err.message : 'Failed to load instances';
            setError(message);
            toaster.error('devices-load-error', 'Failed to load devices', err);
        } finally {
            setLoading(false);
        }
    }, []);

    useEffect(() => {
        loadInstancesAndDevices();
    }, [loadInstancesAndDevices]);

    const createDevice = useCallback((instance: number, name: string, type: DeviceType): void => {
        const newDevice: LocalDevice = {
            id: { type, name },
            type,
            inputPipelines: [],
            outputPipelines: [],
            vlanId: type === 'vlan' ? 0 : undefined,
            isNew: true,
            isDirty: true,
        };

        setInstances(prev => prev.map(inst => {
            if (inst.instance === instance) {
                return {
                    ...inst,
                    devices: [...inst.devices, newDevice],
                };
            }
            return inst;
        }));
    }, []);

    const updateDevice = useCallback((
        instance: number,
        deviceName: string,
        updates: Partial<LocalDevice>
    ): void => {
        setInstances(prev => prev.map(inst => {
            if (inst.instance === instance) {
                return {
                    ...inst,
                    devices: inst.devices.map(device => {
                        if (device.id.name === deviceName) {
                            return {
                                ...device,
                                ...updates,
                                isDirty: true,
                            };
                        }
                        return device;
                    }),
                };
            }
            return inst;
        }));
    }, []);

    const saveDevice = useCallback(async (
        instance: number,
        device: LocalDevice
    ): Promise<boolean> => {
        try {
            const devicePayload = {
                input: device.inputPipelines.map(p => ({
                    name: p.name,
                    weight: parseWeight(p.weight),
                })),
                output: device.outputPipelines.map(p => ({
                    name: p.name,
                    weight: parseWeight(p.weight),
                })),
            };

            const target = {
                instance,
                name: device.id.name,
            };

            let response;
            if (device.type === 'vlan') {
                response = await API.devices.updateVlan({
                    target,
                    device: devicePayload,
                    vlan: device.vlanId ?? 0,
                });
            } else {
                response = await API.devices.updatePlain({
                    target,
                    device: devicePayload,
                });
            }

            if (response.error) {
                throw new Error(response.error);
            }

            // Mark device as clean
            setInstances(prev => prev.map(inst => {
                if (inst.instance === instance) {
                    return {
                        ...inst,
                        devices: inst.devices.map(d => {
                            if (d.id.name === device.id.name) {
                                return { ...d, isDirty: false, isNew: false };
                            }
                            return d;
                        }),
                    };
                }
                return inst;
            }));

            toaster.success('device-save-success', `Device "${device.id.name}" saved`);
            return true;
        } catch (err) {
            toaster.error('device-save-error', `Failed to save device "${device.id.name}"`, err);
            return false;
        }
    }, []);

    const loadPipelineList = useCallback(async (instance: number): Promise<PipelineId[]> => {
        try {
            const response = await API.pipelines.list({ instance });
            return response.ids || [];
        } catch (err) {
            toaster.error('pipeline-list-error', 'Failed to load pipeline list', err);
            return [];
        }
    }, []);

    return {
        instances,
        loading,
        error,
        reloadInstances: loadInstancesAndDevices,
        createDevice,
        updateDevice,
        saveDevice,
        loadPipelineList,
    };
};
