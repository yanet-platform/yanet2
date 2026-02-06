import React, { useMemo } from 'react';
import { Box, Text, Label } from '@gravity-ui/uikit';
import type { TableColumnConfig } from '@gravity-ui/uikit';
import { HardDrive } from '@gravity-ui/icons';
import type { InstanceInfo, DevicePipelineInfo } from '../../../api/inspect';
import { useDeviceCounters, type DeviceAbsoluteData } from '../../../hooks';
import { SortableDataTable } from '../../../components';
import { compareNullableStrings } from '../../../utils/sorting';
import { InspectSection } from '../InspectSection';
import { formatBytes } from '../utils';
import '../inspect.scss';

export interface DevicesSectionProps {
    instance: InstanceInfo;
}

interface DeviceRowData {
    [key: string]: unknown;
    name: string;
    type: string;
    input_pipelines: DevicePipelineInfo[];
    output_pipelines: DevicePipelineInfo[];
    absoluteCounters: DeviceAbsoluteData | undefined;
}

// Format pipeline names as comma-separated list
const formatPipelines = (pipelines: DevicePipelineInfo[]): string => {
    if (pipelines.length === 0) return '-';
    return pipelines.map(p => p.name || 'unnamed').join(', ');
};

// Format packet count for display
const formatPackets = (value: number): string => {
    return Math.floor(value).toLocaleString();
};

// Format bytes from number (interpolated value) for display
const formatBytesFromNumber = (value: number): string => {
    return formatBytes(BigInt(Math.floor(value)));
};

export const DevicesSection: React.FC<DevicesSectionProps> = ({ instance }) => {
    const devices = instance.devices ?? [];

    // Extract device names for the counter hook
    const deviceNames = useMemo(() => {
        return devices.map((device, idx) => device.name || `Device ${idx}`);
    }, [devices]);

    // Use interpolated device counters
    const { absoluteCounters: absoluteCountersMap } = useDeviceCounters(deviceNames, devices.length > 0);

    // Transform devices to row data
    const rowData: DeviceRowData[] = useMemo(() => {
        return devices.map((device, idx) => {
            const deviceName = device.name || `Device ${idx}`;
            return {
                name: deviceName,
                type: device.type || '-',
                input_pipelines: device.input_pipelines ?? [],
                output_pipelines: device.output_pipelines ?? [],
                absoluteCounters: absoluteCountersMap.get(deviceName),
            };
        });
    }, [devices, absoluteCountersMap]);

    const columns = useMemo((): TableColumnConfig<DeviceRowData>[] => [
        {
            id: 'name',
            name: 'Device',
            meta: {
                sort: (a: DeviceRowData, b: DeviceRowData) => compareNullableStrings(a.name, b.name),
            },
            template: (item: DeviceRowData) => (
                <Text variant="body-1" className="devices-table__name">
                    {item.name}
                </Text>
            ),
        },
        {
            id: 'type',
            name: 'Type',
            meta: {
                sort: (a: DeviceRowData, b: DeviceRowData) => compareNullableStrings(a.type, b.type),
            },
            template: (item: DeviceRowData) => (
                <Label theme="info" size="s">{item.type}</Label>
            ),
        },
        {
            id: 'rx',
            name: 'RX',
            template: (item: DeviceRowData) => {
                const counters = item.absoluteCounters;
                if (!counters) {
                    return (
                        <Box className="devices-table__counter devices-table__counter--loading">
                            <Text variant="body-2" color="hint">-- pkts</Text>
                            <Text variant="caption-2" color="hint">-- B</Text>
                        </Box>
                    );
                }
                return (
                    <Box className="devices-table__counter">
                        <Text variant="body-2">{formatPackets(counters.rx.packets)} pkts</Text>
                        <Text variant="caption-2" color="secondary">{formatBytesFromNumber(counters.rx.bytes)}</Text>
                    </Box>
                );
            },
        },
        {
            id: 'tx',
            name: 'TX',
            template: (item: DeviceRowData) => {
                const counters = item.absoluteCounters;
                if (!counters) {
                    return (
                        <Box className="devices-table__counter devices-table__counter--loading">
                            <Text variant="body-2" color="hint">-- pkts</Text>
                            <Text variant="caption-2" color="hint">-- B</Text>
                        </Box>
                    );
                }
                return (
                    <Box className="devices-table__counter">
                        <Text variant="body-2">{formatPackets(counters.tx.packets)} pkts</Text>
                        <Text variant="caption-2" color="secondary">{formatBytesFromNumber(counters.tx.bytes)}</Text>
                    </Box>
                );
            },
        },
        {
            id: 'input_pipelines',
            name: 'Input Pipelines',
            template: (item: DeviceRowData) => (
                <Text variant="body-2" color={item.input_pipelines.length === 0 ? 'secondary' : undefined}>
                    {formatPipelines(item.input_pipelines)}
                </Text>
            ),
        },
        {
            id: 'output_pipelines',
            name: 'Output Pipelines',
            template: (item: DeviceRowData) => (
                <Text variant="body-2" color={item.output_pipelines.length === 0 ? 'secondary' : undefined}>
                    {formatPipelines(item.output_pipelines)}
                </Text>
            ),
        },
    ], []);

    return (
        <InspectSection
            title="Devices"
            icon={HardDrive}
            count={devices.length}
            variant="devices"
            collapsible
            defaultExpanded
        >
            {devices.length > 0 ? (
                <Box className="devices-table-wrapper">
                    <SortableDataTable
                        data={rowData}
                        columns={columns as any}
                        width="max"
                    />
                </Box>
            ) : (
                <Text variant="body-1" color="secondary" className="inspect-text--block">
                    No devices
                </Text>
            )}
        </InspectSection>
    );
};
