import { useState, useCallback, useEffect } from 'react';
import { API } from '../../api';
import type { DeviceType } from '../../api/devices';
import type { PipelineId } from '../../api/pipelines';
import type { InspectResponse, DeviceInfo } from '../../api/inspect';
import { toaster } from '../../utils';
import type { LocalDevice } from './types';

export interface UseDeviceDataResult {
    devices: LocalDevice[];
    loading: boolean;
    error: string | null;
    reloadDevices: () => Promise<void>;
    createDevice: (name: string, type: DeviceType) => void;
    updateDevice: (deviceName: string, updates: Partial<LocalDevice>) => void;
    saveDevice: (device: LocalDevice) => Promise<boolean>;
    loadPipelineList: () => Promise<PipelineId[]>;
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
        inputPipelines: (info.input_pipelines || []).map(p => ({
            name: p.name,
            weight: parseWeight(p.weight),
        })),
        outputPipelines: (info.output_pipelines || []).map(p => ({
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
    const [devices, setDevices] = useState<LocalDevice[]>([]);
    const [loading, setLoading] = useState(true);
    const [error, setError] = useState<string | null>(null);

    const loadDevices = useCallback(async (): Promise<void> => {
        setLoading(true);
        setError(null);

        try {
            // Get devices from inspect
            const inspectResponse: InspectResponse = await API.inspect.inspect();
            const instanceInfo = inspectResponse.instance_info;

            const loadedDevices = instanceInfo?.devices?.map(deviceInfoToLocal) || [];
            setDevices(loadedDevices);
        } catch (err) {
            const message = err instanceof Error ? err.message : 'Failed to load devices';
            setError(message);
            toaster.error('devices-load-error', 'Failed to load devices', err);
        } finally {
            setLoading(false);
        }
    }, []);

    useEffect(() => {
        loadDevices();
    }, [loadDevices]);

    const createDevice = useCallback((name: string, type: DeviceType): void => {
        const newDevice: LocalDevice = {
            id: { type, name },
            type,
            inputPipelines: [],
            outputPipelines: [],
            vlanId: type === 'vlan' ? 0 : undefined,
            isNew: true,
            isDirty: true,
        };

        setDevices(prev => [...prev, newDevice]);
    }, []);

    const updateDevice = useCallback((
        deviceName: string,
        updates: Partial<LocalDevice>
    ): void => {
        setDevices(prev => prev.map(device => {
            if (device.id.name === deviceName) {
                return {
                    ...device,
                    ...updates,
                    isDirty: true,
                };
            }
            return device;
        }));
    }, []);

    const saveDevice = useCallback(async (
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

            let response;
            if (device.type === 'vlan') {
                response = await API.devices.updateVlan({
                    name: device.id.name,
                    device: devicePayload,
                    vlan: device.vlanId ?? 0,
                });
            } else {
                response = await API.devices.updatePlain({
                    name: device.id.name,
                    device: devicePayload,
                });
            }

            if (response.error) {
                throw new Error(response.error);
            }

            // Mark device as clean
            setDevices(prev => prev.map(d => {
                if (d.id.name === device.id.name) {
                    return { ...d, isDirty: false, isNew: false };
                }
                return d;
            }));

            toaster.success('device-save-success', `Device "${device.id.name}" saved`);
            return true;
        } catch (err) {
            toaster.error('device-save-error', `Failed to save device "${device.id.name}"`, err);
            return false;
        }
    }, []);

    const loadPipelineList = useCallback(async (): Promise<PipelineId[]> => {
        try {
            const response = await API.pipelines.list({});
            return response.ids || [];
        } catch (err) {
            toaster.error('pipeline-list-error', 'Failed to load pipeline list', err);
            return [];
        }
    }, []);

    return {
        devices,
        loading,
        error,
        reloadDevices: loadDevices,
        createDevice,
        updateDevice,
        saveDevice,
        loadPipelineList,
    };
};
