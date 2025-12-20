import React, { useState, useCallback } from 'react';
import { Box } from '@gravity-ui/uikit';
import { PageLayout, PageLoader, EmptyState } from '../components';
import type { DeviceType } from '../api/devices';
import {
    DevicePageHeader,
    DeviceCard,
    CreateDeviceDialog,
    useDeviceData,
} from './devices';

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

    const handleCreateDevice = useCallback(() => {
        setCreateDialogOpen(true);
    }, []);

    const handleCreateConfirm = useCallback((name: string, type: DeviceType) => {
        createDevice(name, type);
        setCreateDialogOpen(false);
    }, [createDevice]);

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
            <Box style={{
                width: '100%',
                flex: 1,
                minWidth: 0,
                padding: '20px',
                display: 'flex',
                flexDirection: 'column',
                overflow: 'hidden',
            }}>
                <Box style={{
                    display: 'flex',
                    flexDirection: 'column',
                    gap: '16px',
                    overflowY: 'auto',
                    flex: 1,
                    minHeight: 0,
                }}>
                    {devices.length === 0 ? (
                        <EmptyState message="No devices found. Click 'Create Device' to add one." />
                    ) : (
                        devices.map((device) => (
                            <DeviceCard
                                key={device.id.name}
                                device={device}
                                loadPipelineList={loadPipelineList}
                                onUpdate={(updates) => updateDevice(device.id.name || '', updates)}
                                onSave={() => saveDevice(device)}
                            />
                        ))
                    )}
                </Box>
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
