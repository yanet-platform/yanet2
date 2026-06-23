import { useState, useCallback, useEffect, useRef } from 'react';
import { API } from '@yanet/core/api';
import { parseWeight, type DeviceType } from '@yanet/core/api/devices';
import type { PipelineId } from '@yanet/core/api/pipelines';
import type { InspectResponse, DeviceInfo } from '@yanet/core/api/inspect';
import { deviceTypeManifest } from '@yanet/core/registry';
import { toaster } from '@yanet/core/utils';
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
    loadDeviceExt: (device: LocalDevice) => Promise<void>;
    getServerDevice: (name: string) => LocalDevice | null;
}

// Returns true when two devices have equal type and pipeline arrays in order.
//
// Type-specific ext is compared separately by each type's extDirty hook.
const pipelinesEqual = (a: LocalDevice, b: LocalDevice): boolean => {
    if (a.type !== b.type) return false;
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

// Recompute the dirty flag across pipelines and the type-specific ext.
const computeDirty = (device: LocalDevice, snapshot: LocalDevice | undefined): boolean => {
    if (device.isNew || !snapshot || !pipelinesEqual(device, snapshot)) {
        return true;
    }
    return deviceTypeManifest(device.type)?.extDirty?.(device, snapshot) ?? false;
};

const deviceInfoToLocal = (info: DeviceInfo): LocalDevice => {
    const type = info.type ?? '';
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
        loaded: !deviceTypeManifest(type)?.loadData,
        ext: {},
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
        const extDefaults = deviceTypeManifest(type)?.createDefaults?.();
        const newDevice: LocalDevice = {
            id: { type, name },
            type,
            inputPipelines: [],
            outputPipelines: [],
            isNew: true,
            isDirty: true,
            loaded: true,
            ext: extDefaults ? { [type]: extDefaults } : {},
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
                const snapshot = serverSnapshotRef.current.get(deviceName);
                return { ...updated, isDirty: computeDirty(updated, snapshot) };
            }
            return device;
        }));
    }, []);

    // loadDeviceExt hydrates a device's type-specific ext from the server the
    // first time it is opened, for types that declare a loadData hook.
    //
    // Types without loadData and already-loaded devices are a no-op.
    const loadDeviceExt = useCallback(async (device: LocalDevice): Promise<void> => {
        const manifest = deviceTypeManifest(device.type);
        if (!manifest?.loadData || device.loaded) {
            return;
        }

        const name = device.id.name || '';
        const ext = await manifest.loadData(device);

        // Refresh the clean snapshot first so the dirty check below compares
        // against the freshly loaded server ext, not the empty baseline.
        const snapshot = serverSnapshotRef.current.get(name);
        if (snapshot) {
            const newSnapshot: LocalDevice = {
                ...snapshot,
                ext: { ...snapshot.ext, [device.type]: ext },
                loaded: true,
            };
            serverSnapshotRef.current = new Map(serverSnapshotRef.current).set(name, newSnapshot);
        }

        setDevices(prev => prev.map(d => {
            if (d.id.name !== name) return d;
            const next: LocalDevice = {
                ...d,
                ext: { ...d.ext, [device.type]: ext },
                loaded: true,
            };
            return { ...next, isDirty: computeDirty(next, serverSnapshotRef.current.get(name)) };
        }));
    }, []);

    const saveDevice = useCallback(async (
        device: LocalDevice
    ): Promise<boolean> => {
        const name = device.id.name || '';
        const manifest = deviceTypeManifest(device.type);
        if (!manifest) {
            toaster.error('device-save-error', `Unknown device type "${device.type}"`);
            return false;
        }

        try {
            const snapshot = serverSnapshotRef.current.get(name);
            const ext = await manifest.save(device, snapshot);

            const savedDevice: LocalDevice = {
                ...device,
                isDirty: false,
                isNew: false,
                loaded: true,
                ext: { ...device.ext, [device.type]: ext },
            };
            if (name) {
                serverSnapshotRef.current = new Map(serverSnapshotRef.current).set(name, savedDevice);
            }
            setDevices(prev => prev.map(d => (d.id.name === name ? savedDevice : d)));

            toaster.success('device-save-success', `Device "${name}" saved`);
            return true;
        } catch (err) {
            toaster.error('device-save-error', `Failed to save device "${name}"`, err);
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
        loadDeviceExt,
        getServerDevice,
    };
};
