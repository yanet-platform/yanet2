import { useState, useCallback, useEffect, useRef } from 'react';
import { API } from '../../../api';
import type { DeviceType } from '../../../api/devices';
import type { PipelineId } from '../../../api/pipelines';
import type { InspectResponse, DeviceInfo } from '../../../api/inspect';
import { toaster } from '../../../utils';
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
    getServerDevice: (name: string) => LocalDevice | null;
}

const parseWeight = (weight: string | number | undefined): number => {
    if (weight === undefined) return 0;
    if (typeof weight === 'number') return weight;
    return parseInt(weight, 10) || 0;
};

/** Returns true when two LocalDevice values are structurally equal (same type, vlanId, and pipeline arrays in order). */
const localDeviceEquals = (a: LocalDevice, b: LocalDevice): boolean => {
    if (a.type !== b.type) return false;
    if (a.vlanId !== b.vlanId) return false;
    if (a.inputPipelines.length !== b.inputPipelines.length) return false;
    if (a.outputPipelines.length !== b.outputPipelines.length) return false;
    for (let idx = 0; idx < a.inputPipelines.length; idx++) {
        const pa = a.inputPipelines[idx];
        const pb = b.inputPipelines[idx];
        if (pa.name !== pb.name || Number(pa.weight) !== Number(pb.weight)) return false;
    }
    for (let idx = 0; idx < a.outputPipelines.length; idx++) {
        const pa = a.outputPipelines[idx];
        const pb = b.outputPipelines[idx];
        if (pa.name !== pb.name || Number(pa.weight) !== Number(pb.weight)) return false;
    }
    return true;
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
 * Hook for managing device data and API interactions.
 * Maintains a server-side snapshot map used by the diff modal.
 */
export const useDeviceData = (): UseDeviceDataResult => {
    const [devices, setDevices] = useState<LocalDevice[]>([]);
    const [loading, setLoading] = useState(true);
    const [error, setError] = useState<string | null>(null);
    const serverSnapshotRef = useRef<Map<string, LocalDevice>>(new Map());

    const loadDevices = useCallback(async (): Promise<void> => {
        setLoading(true);
        setError(null);

        try {
            const inspectResponse: InspectResponse = await API.inspect.inspect();
            const instanceInfo = inspectResponse.instance_info;

            const loadedDevices = instanceInfo?.devices?.map(deviceInfoToLocal) || [];

            const snapshot = new Map<string, LocalDevice>();
            for (const d of loadedDevices) {
                if (d.id.name) {
                    snapshot.set(d.id.name, d);
                }
            }
            serverSnapshotRef.current = snapshot;

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
                const updated = { ...device, ...updates };
                const serverSnapshot = serverSnapshotRef.current.get(deviceName);
                const isDirty = updated.isNew
                    || !serverSnapshot
                    || !localDeviceEquals(updated, serverSnapshot);
                return { ...updated, isDirty };
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

            // Mark device as clean and update the server snapshot.
            const savedDevice = { ...device, isDirty: false, isNew: false };
            if (device.id.name) {
                serverSnapshotRef.current = new Map(serverSnapshotRef.current).set(
                    device.id.name,
                    savedDevice,
                );
            }
            setDevices(prev => prev.map(d => {
                if (d.id.name === device.id.name) {
                    return savedDevice;
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

    const getServerDevice = useCallback((name: string): LocalDevice | null => {
        return serverSnapshotRef.current.get(name) ?? null;
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
        getServerDevice,
    };
};
