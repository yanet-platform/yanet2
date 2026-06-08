import React, { useState, useCallback, useMemo, useEffect } from 'react';
import { useSearchParams } from 'react-router-dom';
import { Button, Icon } from '@gravity-ui/uikit';
import { Plus } from '@gravity-ui/icons';
import { useSearchParamHelpers, useListNavigation, usePageContribution } from '../../../hooks';
import { PageLayout, PageLoader, EmptyState, CommandPaletteHeader } from '../../../components';
import type { DeviceType } from '../../../api/devices';
import type { LocalDevice } from './types';
import { useDeviceCounters } from '../../../hooks';
import { useCounterHistory } from '../../../hooks/useCounterHistory';
import { useUnsavedChangesBlocker } from '../_shared/lane-editor';
import type { Command, RowAdapter, ShortcutSection, PagePaletteContribution } from '../../../components/command-palette';
import {
    DevicesList,
    DeviceDetails,
    CreateDeviceDialog,
    useDeviceData,
    filterDevices,
    buildGroups,
} from '.';
import type { FilterKind } from '.';
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
    const [deviceFilter, setDeviceFilter] = useState<FilterKind>('all');

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

    const visibleDevices = useMemo(
        () => buildGroups(filterDevices(devices, deviceFilter), grouping).flatMap(g => g.items),
        [devices, deviceFilter, grouping],
    );

    const anyDirty = useMemo(() => devices.some(d => d.isDirty), [devices]);
    useUnsavedChangesBlocker(anyDirty);

    const { updateParams } = useSearchParamHelpers(setSearchParams);

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

    const visibleNavRows = useMemo(
        () => visibleDevices.map(d => ({ id: d.id.name || '' })),
        [visibleDevices],
    );

    useListNavigation({
        rows: visibleNavRows,
        activeId: selectedDeviceName,
        setActiveId: (id) => { if (id) handleSelectDevice(id); },
        getElementId: (name) => `dv-row-${name}`,
    });

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

    const canCreate = !loading && !(error && devices.length === 0);

    const commands = useMemo((): Command[] => {
        const list: Command[] = [];
        if (canCreate) {
            list.push({
                id: '__create_device',
                icon: '+',
                label: 'Create device',
                sub: 'Open the create device dialog',
                keywords: 'create new device add',
                onSelect: () => handleCreateDevice(),
            });
        }
        if (selectedDevice?.isDirty) {
            list.push({
                id: '__save_device',
                icon: '✓',
                label: 'Save current device',
                sub: `Save "${selectedDevice.id.name || ''}"`,
                keywords: 'save commit apply device',
                onSelect: () => handleSaveDevice(),
            });
        }
        list.push({
            id: '__group_flat',
            icon: '≡',
            label: 'Group: Flat',
            keywords: 'group flat list ungrouped',
            onSelect: () => setGrouping('flat'),
        });
        list.push({
            id: '__group_type',
            icon: '≡',
            label: 'Group: By type',
            keywords: 'group type physical vlan',
            onSelect: () => setGrouping('type'),
        });
        list.push({
            id: '__group_parent',
            icon: '≡',
            label: 'Group: By parent',
            keywords: 'group parent hierarchy',
            onSelect: () => setGrouping('parent'),
        });
        return list;
    }, [canCreate, selectedDevice, handleCreateDevice, handleSaveDevice]);

    const rowAdapter = useMemo((): RowAdapter<LocalDevice> => ({
        rows: devices,
        getId: (d) => d.id.name || '',
        getLabel: (d) => d.id.name || '(unnamed)',
        getSub: (d) => d.type === 'vlan'
            ? `vlan${d.vlanId !== undefined ? ' · ' + d.vlanId : ''}`
            : 'physical',
        searchText: (d) => [d.id.name, d.type, d.vlanId].filter(Boolean).join(' '),
        onSelect: (id) => { setDeviceFilter('all'); handleSelectDevice(id); },
        icon: '→',
    }), [devices, handleSelectDevice]);

    const shortcuts = useMemo((): ShortcutSection[] => [{
        title: 'Devices',
        items: [{ keys: '↑ ↓', desc: 'Select previous / next device' }],
    }], []);

    const contribution = useMemo<PagePaletteContribution>(() => ({
        commands,
        rowAdapter: rowAdapter as RowAdapter<unknown>,
        placeholder: 'Search devices or run an action…',
        shortcuts,
    }), [commands, rowAdapter, shortcuts]);
    usePageContribution(contribution);

    const existingDeviceNames = devices.map(d => d.id.name || '');

    const selectedCounterData = selectedDevice?.id.name
        ? counters.get(selectedDevice.id.name)
        : undefined;

    const selectedHistory = selectedDevice?.id.name
        ? history.get(selectedDevice.id.name)
        : undefined;

    const headerContent = (
        <CommandPaletteHeader
            title="Devices"
            placeholder="Search devices or run an action…"
            actions={<Button view="action" onClick={handleCreateDevice} disabled={!canCreate}>
                <Icon data={Plus} size={16} />
                Create Device
            </Button>}
        />
    );

    return (
        <PageLayout header={headerContent}>
            {loading ? (
                <PageLoader loading={loading} size="l" />
            ) : (error && devices.length === 0) ? (
                <EmptyState message={error} />
            ) : (
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
                            filter={deviceFilter}
                            onFilterChange={setDeviceFilter}
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
            )}

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
