import React, { useState, useCallback, useMemo } from 'react';
import { Box } from '@gravity-ui/uikit';
import { PageLayout, PageLoader } from '../components';
import type { DeviceType } from '../api/devices';
import {
    DevicePageHeader,
    DevicesList,
    DeviceDetails,
    CreateDeviceDialog,
    useDeviceData,
} from './devices';
import './devices/devices.scss';

const DevicesPage: React.FC = () => {
    const {
        devices,
        loading,
        createDevice,
        updateDevice,
        saveDevice,
        loadPipelineList,
    } = useDeviceData();

    const [createDialogOpen, setCreateDialogOpen] = useState(false);
    const [selectedDeviceName, setSelectedDeviceName] = useState<string | null>(null);

    const selectedDevice = useMemo(() => {
        if (!selectedDeviceName) return null;
        return devices.find(d => d.id.name === selectedDeviceName) || null;
    }, [devices, selectedDeviceName]);

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

    const handleUpdateDevice = useCallback((updates: Partial<typeof selectedDevice>) => {
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

    const headerContent = (
        <DevicePageHeader onCreateDevice={handleCreateDevice} />
    );

    if (loading) {
        return (
            <PageLayout title="Devices">
                <PageLoader loading={loading} size="l" />
            </PageLayout>
        );
    }

    return (
        <PageLayout header={headerContent}>
            <Box className="devices-page__layout">
                <DevicesList
                    devices={devices}
                    selectedDeviceName={selectedDeviceName}
                    onSelectDevice={handleSelectDevice}
                />
                <DeviceDetails
                    device={selectedDevice}
                    loadPipelineList={loadPipelineList}
                    onUpdate={handleUpdateDevice}
                    onSave={handleSaveDevice}
                />
            </Box>

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
