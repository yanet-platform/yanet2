import React, { useState, useCallback, useMemo, useEffect } from 'react';
import { useSearchParams } from 'react-router-dom';
import { PageLayout, PageLoader, EmptyState } from '../../../components';
import type { DeviceType } from '../../../api/devices';
import type { LocalDevice } from './types';
import { useDeviceCounters } from '../../../hooks';
import { useCounterHistory } from '../../../hooks/useCounterHistory';
import { useUnsavedChangesBlocker } from '../_shared/lane-editor';
import {
    DevicePageHeader,
    DevicesList,
    DeviceDetails,
    CreateDeviceDialog,
    useDeviceData,
} from '.';
import './devices.scss';

type GroupingMode = 'flat' | 'type' | 'parent';

const QP_DEVICE = 'device';

const DevicesPage: React.FC = () => {
    const {
        devices,
        loading,
        error,
        createDevice,
        updateDevice,
        saveDevice,
        loadPipelineList,
        getServerDevice,
    } = useDeviceData();

    const [searchParams, setSearchParams] = useSearchParams();
    const [createDialogOpen, setCreateDialogOpen] = useState(false);
    const [grouping, setGrouping] = useState<GroupingMode>('type');
    const [searchQuery, setSearchQuery] = useState('');

    const deviceNames = useMemo(() => devices.map(d => d.id.name || ''), [devices]);

    const queryDevice = useMemo(() => searchParams.get(QP_DEVICE), [searchParams]);

    const selectedDeviceName = useMemo((): string | null => {
        if (queryDevice && (loading || deviceNames.includes(queryDevice))) {
            return queryDevice;
        }
        return deviceNames[0] ?? null;
    }, [deviceNames, queryDevice, loading]);

    const selectedDevice = useMemo(() => {
        if (!selectedDeviceName) return null;
        return devices.find(d => d.id.name === selectedDeviceName) || null;
    }, [devices, selectedDeviceName]);

    const anyDirty = useMemo(() => devices.some(d => d.isDirty), [devices]);
    useUnsavedChangesBlocker(anyDirty);

    const updateParams = useCallback((updates: Record<string, string | null>): void => {
        setSearchParams((prev) => {
            const next = new URLSearchParams(prev);
            for (const [key, value] of Object.entries(updates)) {
                if (value === null || value === '') {
                    next.delete(key);
                } else {
                    next.set(key, value);
                }
            }
            return next;
        }, { replace: true });
    }, [setSearchParams]);

    useEffect(() => {
        if (loading) return;
        if (!selectedDeviceName) {
            if (queryDevice !== null) {
                updateParams({ [QP_DEVICE]: null });
            }
        } else if (queryDevice !== selectedDeviceName) {
            updateParams({ [QP_DEVICE]: selectedDeviceName });
        }
    }, [selectedDeviceName, loading, queryDevice, deviceNames.length, updateParams]);

    const { counters } = useDeviceCounters(deviceNames, deviceNames.length > 0);
    const history = useCounterHistory(counters);

    const handleCreateDevice = useCallback(() => {
        setCreateDialogOpen(true);
    }, []);

    const handleCreateConfirm = useCallback((name: string, type: DeviceType) => {
        createDevice(name, type);
        setCreateDialogOpen(false);
        updateParams({ [QP_DEVICE]: name || null });
    }, [createDevice, updateParams]);

    const handleSelectDevice = useCallback((deviceName: string) => {
        updateParams({ [QP_DEVICE]: deviceName || null });
    }, [updateParams]);

    const handleUpdateDevice = useCallback((updates: Partial<LocalDevice>) => {
        if (selectedDeviceName) {
            updateDevice(selectedDeviceName, updates);
        }
    }, [selectedDeviceName, updateDevice]);

    const handleSaveDevice = useCallback(async () => {
        if (selectedDevice) {
            return saveDevice(selectedDevice);
        }
        return false;
    }, [selectedDevice, saveDevice]);

    const existingDeviceNames = devices.map(d => d.id.name || '');

    const selectedCounterData = selectedDevice?.id.name
        ? counters.get(selectedDevice.id.name)
        : undefined;

    const selectedHistory = selectedDevice?.id.name
        ? history.get(selectedDevice.id.name)
        : undefined;

    const headerContent = (
        <DevicePageHeader
            onCreateDevice={handleCreateDevice}
            searchQuery={searchQuery}
            onSearchChange={setSearchQuery}
        />
    );

    if (loading) {
        return (
            <PageLayout title="Devices">
                <PageLoader loading={loading} size="l" />
            </PageLayout>
        );
    }

    if (error && devices.length === 0) {
        return (
            <PageLayout title="Devices">
                <EmptyState message={error} />
            </PageLayout>
        );
    }

    return (
        <PageLayout header={headerContent}>
            <div className="devices-page-v2">
                <div className="dv-workspace">
                    <DevicesList
                        devices={devices}
                        selectedDeviceName={selectedDeviceName}
                        grouping={grouping}
                        onGroupingChange={setGrouping}
                        onSelectDevice={handleSelectDevice}
                        counters={counters}
                        history={history}
                        query={searchQuery}
                    />
                    <DeviceDetails
                        device={selectedDevice}
                        loadPipelineList={loadPipelineList}
                        counterData={selectedCounterData}
                        history={selectedHistory}
                        onUpdate={handleUpdateDevice}
                        onSave={handleSaveDevice}
                        getServerDevice={getServerDevice}
                    />
                </div>
            </div>

            <CreateDeviceDialog
                open={createDialogOpen}
                onClose={() => setCreateDialogOpen(false)}
                onConfirm={handleCreateConfirm}
                existingNames={existingDeviceNames}
            />
        </PageLayout>
    );
};

export default DevicesPage;
