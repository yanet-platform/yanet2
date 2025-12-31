import React, { useEffect, useMemo, useState } from 'react';
import { Box, Text, Label } from '@gravity-ui/uikit';
import type { TableColumnConfig } from '@gravity-ui/uikit';
import { HardDrive } from '@gravity-ui/icons';
import type { InstanceInfo, DevicePipelineInfo } from '../../../api/inspect';
import { API, type CountersResponse, type CounterInfo } from '../../../api';
import { SortableDataTable } from '../../../components';
import { compareNullableStrings } from '../../../utils/sorting';
import { InspectSection } from '../InspectSection';
import { formatBytes } from '../utils';
import '../inspect.scss';

export interface DevicesSectionProps {
    instance: InstanceInfo;
}

interface DeviceCounters {
    rxPackets: bigint;
    rxBytes: bigint;
    txPackets: bigint;
    txBytes: bigint;
}

interface DeviceRowData {
    [key: string]: unknown;
    name: string;
    type: string;
    inputPipelines: DevicePipelineInfo[];
    outputPipelines: DevicePipelineInfo[];
    counters: DeviceCounters | null;
}

// Helper to sum counter values across all instances
const sumCounterValues = (counter: CounterInfo | undefined): bigint => {
    if (!counter?.instances) return BigInt(0);
    return counter.instances.reduce((sum, inst) => {
        const val = inst.values?.[0];
        return sum + BigInt(val ?? 0);
    }, BigInt(0));
};

// Helper to find counter by name
const findCounter = (counters: CounterInfo[] | undefined, name: string): CounterInfo | undefined => {
    return counters?.find(c => c.name === name);
};

// Extract counters from response
const extractCounters = (response: CountersResponse): DeviceCounters => ({
    rxPackets: sumCounterValues(findCounter(response.counters, 'rx')),
    rxBytes: sumCounterValues(findCounter(response.counters, 'rx_bytes')),
    txPackets: sumCounterValues(findCounter(response.counters, 'tx')),
    txBytes: sumCounterValues(findCounter(response.counters, 'tx_bytes')),
});

// Format pipeline names as comma-separated list
const formatPipelines = (pipelines: DevicePipelineInfo[]): string => {
    if (pipelines.length === 0) return '-';
    return pipelines.map(p => p.name || 'unnamed').join(', ');
};

// Format counter value for display
const formatPackets = (value: bigint): string => {
    return value.toLocaleString();
};

export const DevicesSection: React.FC<DevicesSectionProps> = ({ instance }) => {
    const devices = instance.devices ?? [];
    const [countersMap, setCountersMap] = useState<Map<string, DeviceCounters>>(new Map());
    const [loading, setLoading] = useState(true);

    // Batch fetch all device counters
    useEffect(() => {
        if (devices.length === 0) {
            setLoading(false);
            return;
        }

        const fetchAllCounters = async () => {
            const results = new Map<string, DeviceCounters>();

            await Promise.all(
                devices.map(async (device, idx) => {
                    const deviceName = device.name || `Device ${idx}`;
                    try {
                        const response = await API.counters.device({ device: deviceName });
                        results.set(deviceName, extractCounters(response));
                    } catch (error) {
                        console.error(`Failed to fetch counters for ${deviceName}:`, error);
                    }
                })
            );

            setCountersMap(results);
            setLoading(false);
        };

        fetchAllCounters();
    }, [devices]);

    // Transform devices to row data
    const rowData: DeviceRowData[] = useMemo(() => {
        return devices.map((device, idx) => {
            const deviceName = device.name || `Device ${idx}`;
            return {
                name: deviceName,
                type: device.type || '-',
                inputPipelines: device.inputPipelines ?? [],
                outputPipelines: device.outputPipelines ?? [],
                counters: countersMap.get(deviceName) ?? null,
            };
        });
    }, [devices, countersMap]);

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
                if (!item.counters) return <Text color="secondary">-</Text>;
                return (
                    <Box className="devices-table__counter">
                        <Text variant="body-2">{formatPackets(item.counters.rxPackets)} pkts</Text>
                        <Text variant="caption-2" color="secondary">{formatBytes(item.counters.rxBytes)}</Text>
                    </Box>
                );
            },
        },
        {
            id: 'tx',
            name: 'TX',
            template: (item: DeviceRowData) => {
                if (!item.counters) return <Text color="secondary">-</Text>;
                return (
                    <Box className="devices-table__counter">
                        <Text variant="body-2">{formatPackets(item.counters.txPackets)} pkts</Text>
                        <Text variant="caption-2" color="secondary">{formatBytes(item.counters.txBytes)}</Text>
                    </Box>
                );
            },
        },
        {
            id: 'inputPipelines',
            name: 'Input Pipelines',
            template: (item: DeviceRowData) => (
                <Text variant="body-2" color={item.inputPipelines.length === 0 ? 'secondary' : undefined}>
                    {formatPipelines(item.inputPipelines)}
                </Text>
            ),
        },
        {
            id: 'outputPipelines',
            name: 'Output Pipelines',
            template: (item: DeviceRowData) => (
                <Text variant="body-2" color={item.outputPipelines.length === 0 ? 'secondary' : undefined}>
                    {formatPipelines(item.outputPipelines)}
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
                    {loading ? (
                        <Text variant="body-1" color="secondary">Loading counters...</Text>
                    ) : (
                        <SortableDataTable
                            data={rowData}
                            columns={columns as any}
                            width="max"
                        />
                    )}
                </Box>
            ) : (
                <Text variant="body-1" color="secondary" className="inspect-text--block">
                    No devices
                </Text>
            )}
        </InspectSection>
    );
};
