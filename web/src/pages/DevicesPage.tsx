import React, { useState, useCallback } from 'react';
import { Box, Alert } from '@gravity-ui/uikit';
import { PageLayout, PageLoader, EmptyState, InstanceTabs } from '../components';
import { useInstanceTabs } from '../hooks';
import type { DeviceType } from '../api/devices';
import {
    DevicePageHeader,
    DeviceCard,
    CreateDeviceDialog,
    useDeviceData,
} from './devices';

const DevicesPage: React.FC = () => {
    const {
        instances,
        loading,
        error,
        createDevice,
        updateDevice,
        saveDevice,
        loadPipelineList,
    } = useDeviceData();

    const { activeTab, setActiveTab, currentTabIndex } = useInstanceTabs({ items: instances });

    const [createDialogOpen, setCreateDialogOpen] = useState(false);

    const currentInstance = instances[currentTabIndex];
    const currentInstanceNumber = currentInstance?.instance ?? currentTabIndex;

    const handleCreateDevice = useCallback(() => {
        setCreateDialogOpen(true);
    }, []);

    const handleCreateConfirm = useCallback((name: string, type: DeviceType) => {
        createDevice(currentInstanceNumber, name, type);
        setCreateDialogOpen(false);
    }, [createDevice, currentInstanceNumber]);

    const existingDeviceNames = currentInstance?.devices.map(d => d.id.name || '') || [];

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

    if (instances.length === 0) {
        return (
            <PageLayout header={headerContent}>
                {error && (
                    <Box style={{ padding: '12px 20px' }}>
                        <Alert theme="danger" message={error} />
                    </Box>
                )}
                <Box style={{ width: '100%', flex: 1, minWidth: 0, padding: '20px' }}>
                    <EmptyState message="No instances found." />
                </Box>

                <CreateDeviceDialog
                    open={createDialogOpen}
                    onClose={() => setCreateDialogOpen(false)}
                    onConfirm={handleCreateConfirm}
                    existingNames={existingDeviceNames}
                />
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
                {error && (
                    <Box style={{ marginBottom: '12px' }}>
                        <Alert theme="danger" message={error} />
                    </Box>
                )}
                <InstanceTabs
                    items={instances}
                    activeTab={activeTab}
                    onTabChange={setActiveTab}
                    getTabLabel={(inst) => `Instance ${inst.instance}`}
                    renderContent={(inst) => (
                        <Box style={{
                            display: 'flex',
                            flexDirection: 'column',
                            gap: '16px',
                            overflowY: 'auto',
                            flex: 1,
                            minHeight: 0,
                        }}>
                            {inst.devices.length === 0 ? (
                                <EmptyState message="No devices in this instance. Click 'Create Device' to add one." />
                            ) : (
                                inst.devices.map((device) => (
                                    <DeviceCard
                                        key={device.id.name}
                                        device={device}
                                        loadPipelineList={() => loadPipelineList(inst.instance)}
                                        onUpdate={(updates) => updateDevice(inst.instance, device.id.name || '', updates)}
                                        onSave={() => saveDevice(inst.instance, device)}
                                    />
                                ))
                            )}
                        </Box>
                    )}
                    contentStyle={{ flex: 1, minHeight: 0, display: 'flex', flexDirection: 'column' }}
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
