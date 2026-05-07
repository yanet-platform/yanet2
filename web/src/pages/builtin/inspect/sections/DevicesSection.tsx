import React, { useMemo } from 'react';
import { Box } from '@gravity-ui/uikit';
import type { TableColumnConfig } from '@gravity-ui/uikit';
import { Link as RouterLink } from 'react-router-dom';
import type { InstanceInfo, DevicePipelineInfo } from '../../../../api/inspect';
import type { DeviceAbsoluteData, DeviceCounterData } from '../../../../hooks';
import { SortableDataTable, EmptyState } from '../../../../components';
import { compareNullableStrings } from '../../../../utils';
import { InspectCard } from '../InspectCard';
import { Sparkline } from '../Sparkline';
import { StatusPill } from '../StatusPill';
import { fmtPkts, fmtBytes } from '../formatters';
import { useDeviceTrendSeries } from '../hooks';

export interface DevicesSectionProps {
    instance: InstanceInfo;
    rateCounters: Map<string, DeviceCounterData>;
    absoluteCounters: Map<string, DeviceAbsoluteData>;
}

interface DeviceRowData {
    [key: string]: unknown;
    name: string;
    type: string;
    input_pipelines: DevicePipelineInfo[];
    output_pipelines: DevicePipelineInfo[];
    absolute: DeviceAbsoluteData | undefined;
    rxSeries: number[];
    txSeries: number[];
    status: 'ok' | 'idle';
}

export const DevicesSection: React.FC<DevicesSectionProps> = ({
    instance,
    rateCounters,
    absoluteCounters,
}) => {
    const devices = instance.devices ?? [];

    const rxTrend = useDeviceTrendSeries(rateCounters, 'rx');
    const txTrend = useDeviceTrendSeries(rateCounters, 'tx');

    const rowData: DeviceRowData[] = useMemo(() => {
        return devices.map((device, idx) => {
            const name = device.name ?? `device-${idx}`;
            const abs = absoluteCounters.get(name);
            const isOk = !!abs && (abs.rx.packets > 0 || abs.tx.packets > 0);
            return {
                name,
                type: device.type ?? '-',
                input_pipelines: device.input_pipelines ?? [],
                output_pipelines: device.output_pipelines ?? [],
                absolute: abs,
                rxSeries: rxTrend.get(name) ?? [],
                txSeries: txTrend.get(name) ?? [],
                status: isOk ? 'ok' : 'idle',
            };
        });
    }, [devices, absoluteCounters, rxTrend, txTrend]);

    const columns: TableColumnConfig<DeviceRowData>[] = useMemo(() => [
        {
            id: 'name',
            name: 'Device',
            meta: {
                sort: (a: DeviceRowData, b: DeviceRowData) => compareNullableStrings(a.name, b.name),
            },
            template: (item: DeviceRowData) => (
                <span className="inspect-mono">{item.name}</span>
            ),
        },
        {
            id: 'type',
            name: 'Type',
            meta: {
                sort: (a: DeviceRowData, b: DeviceRowData) => compareNullableStrings(a.type, b.type),
            },
            template: (item: DeviceRowData) => (
                <span className="inspect-pill">{item.type}</span>
            ),
        },
        {
            id: 'rx',
            name: 'RX',
            width: 140,
            align: 'right',
            template: (item: DeviceRowData) => {
                const abs = item.absolute;
                if (!abs) {
                    return (
                        <div className="inspect-pkts inspect-pkts--loading">
                            <div className="inspect-pkts-main inspect-num">-- <span>pkts</span></div>
                            <div className="inspect-pkts-sub inspect-num">-- B</div>
                        </div>
                    );
                }
                return (
                    <div className="inspect-pkts">
                        <div className="inspect-pkts-main inspect-num">
                            {fmtPkts(abs.rx.packets)} <span>pkts</span>
                        </div>
                        <div className="inspect-pkts-sub inspect-num">
                            {fmtBytes(abs.rx.bytes)}
                        </div>
                    </div>
                );
            },
        },
        {
            id: 'rxTrend',
            name: 'Trend',
            width: 220,
            template: (item: DeviceRowData) => (
                <Sparkline
                    data={item.rxSeries}
                    color={item.status === 'ok' ? 'var(--inspect-ok)' : 'var(--inspect-idle)'}
                    w={200}
                    h={20}
                />
            ),
        },
        {
            id: 'tx',
            name: 'TX',
            width: 140,
            align: 'right',
            template: (item: DeviceRowData) => {
                const abs = item.absolute;
                if (!abs) {
                    return (
                        <div className="inspect-pkts inspect-pkts--loading">
                            <div className="inspect-pkts-main inspect-num">-- <span>pkts</span></div>
                            <div className="inspect-pkts-sub inspect-num">-- B</div>
                        </div>
                    );
                }
                return (
                    <div className="inspect-pkts">
                        <div className="inspect-pkts-main inspect-num">
                            {fmtPkts(abs.tx.packets)} <span>pkts</span>
                        </div>
                        <div className="inspect-pkts-sub inspect-num">
                            {fmtBytes(abs.tx.bytes)}
                        </div>
                    </div>
                );
            },
        },
        {
            id: 'pipelines',
            name: 'Pipelines (in → out)',
            template: (item: DeviceRowData) => {
                const inName = item.input_pipelines[0]?.name ?? '—';
                const outName = item.output_pipelines[0]?.name ?? '—';
                return (
                    <div className="inspect-pipe-cell">
                        <span className="inspect-pipe-chip">{inName}</span>
                        <span className="inspect-pipe-arrow">→</span>
                        <span className="inspect-pipe-chip">{outName}</span>
                    </div>
                );
            },
        },
        {
            id: 'status',
            name: 'Status',
            template: (item: DeviceRowData) => (
                <StatusPill state={item.status} label={item.status} />
            ),
        },
    ], []);

    const right = (
        <RouterLink to="/devices" className="inspect-link">
            Open all →
        </RouterLink>
    );

    return (
        <InspectCard title="Devices" count={devices.length} right={right}>
            {devices.length > 0 ? (
                <Box className="inspect-table-host">
                    <SortableDataTable data={rowData} columns={columns as any} width="max" />
                </Box>
            ) : (
                <EmptyState message="No devices" compact />
            )}
        </InspectCard>
    );
};
