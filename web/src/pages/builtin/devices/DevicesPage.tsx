import React, { useState, useCallback, useMemo, useEffect, useRef } from 'react';
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

    const [createDialogOpen, setCreateDialogOpen] = useState(false);
    const [selectedDeviceName, setSelectedDeviceName] = useState<string | null>(null);
    const [grouping, setGrouping] = useState<GroupingMode>('type');
    const [searchQuery, setSearchQuery] = useState('');
    const searchRef = useRef<HTMLInputElement>(null);

    useEffect(() => {
        const handleKeyDown = (e: KeyboardEvent): void => {
            if ((e.metaKey || e.ctrlKey) && e.key === 'k') {
                e.preventDefault();
                searchRef.current?.focus();
            }
        };
        window.addEventListener('keydown', handleKeyDown);
        return () => window.removeEventListener('keydown', handleKeyDown);
    }, []);

    const selectedDevice = useMemo(() => {
        if (!selectedDeviceName) return null;
        return devices.find(d => d.id.name === selectedDeviceName) || null;
    }, [devices, selectedDeviceName]);

    const anyDirty = useMemo(() => devices.some(d => d.isDirty), [devices]);
    useUnsavedChangesBlocker(anyDirty);

    const deviceNames = useMemo(() => devices.map(d => d.id.name || ''), [devices]);

    const { counters } = useDeviceCounters(deviceNames, deviceNames.length > 0);
    const history = useCounterHistory(counters);

    const handleCreateDevice = useCallback(() => {
        setCreateDialogOpen(true);
    }, []);

    const handleCreateConfirm = useCallback((name: string, type: DeviceType) => {
        createDevice(name, type);
        setCreateDialogOpen(false);
        setSelectedDeviceName(name);
    }, [createDevice]);

    const handleSelectDevice = useCallback((deviceName: string) => {
        setSelectedDeviceName(deviceName);
    }, []);

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
            searchRef={searchRef}
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
