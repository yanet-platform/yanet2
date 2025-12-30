import React, { useEffect, useState } from 'react';
import { Box, Text } from '@gravity-ui/uikit';
import type { InstanceInfo, DeviceInfo, DevicePipelineInfo } from '../../../api/inspect';
import { API, type CountersResponse, type CounterInfo } from '../../../api';
import { formatUint64, formatBytes } from '../utils';
import '../inspect.scss';

export interface DevicesSectionProps {
    instance: InstanceInfo;
}

interface PipelineRowProps {
    pipeline: DevicePipelineInfo;
}

const PipelineRow: React.FC<PipelineRowProps> = ({ pipeline }) => (
    <Box className="device-card__pipeline-row">
        <Text variant="body-2" className="device-card__pipeline-name">
            {pipeline.name || 'unnamed'}
        </Text>
        <span className="device-card__pipeline-separator" />
        <Text variant="body-2" className="device-card__pipeline-weight">
            {formatUint64(pipeline.weight)}
        </Text>
    </Box>
);

interface PipelineGroupProps {
    label: string;
    pipelines: DevicePipelineInfo[];
}

const PipelineGroup: React.FC<PipelineGroupProps> = ({ label, pipelines }) => {
    if (pipelines.length === 0) return null;

    return (
        <Box className="device-card__section">
            <Text variant="body-2" color="secondary" className="device-card__section-label">
                {label}
            </Text>
            <Box className="device-card__pipeline-list">
                {pipelines.map((pipeline, idx) => (
                    <PipelineRow key={pipeline.name ?? idx} pipeline={pipeline} />
                ))}
            </Box>
        </Box>
    );
};

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

interface CountersDisplayProps {
    counters: CountersResponse | null;
    loading: boolean;
}

const CountersDisplay: React.FC<CountersDisplayProps> = ({ counters, loading }) => {
    if (loading) {
        return (
            <Text variant="body-2" color="secondary">
                Loading counters...
            </Text>
        );
    }

    if (!counters?.counters) {
        return null;
    }

    const rxPackets = sumCounterValues(findCounter(counters.counters, 'rx'));
    const rxBytes = sumCounterValues(findCounter(counters.counters, 'rx_bytes'));
    const txPackets = sumCounterValues(findCounter(counters.counters, 'tx'));
    const txBytes = sumCounterValues(findCounter(counters.counters, 'tx_bytes'));

    return (
        <Box className="device-card__counters">
            <Box className="device-card__counter-row">
                <Text variant="body-2" color="secondary">RX:</Text>
                <Text variant="body-2">{rxPackets.toString()} pkts</Text>
                <Text variant="body-2" color="secondary">/</Text>
                <Text variant="body-2">{formatBytes(rxBytes)}</Text>
            </Box>
            <Box className="device-card__counter-row">
                <Text variant="body-2" color="secondary">TX:</Text>
                <Text variant="body-2">{txPackets.toString()} pkts</Text>
                <Text variant="body-2" color="secondary">/</Text>
                <Text variant="body-2">{formatBytes(txBytes)}</Text>
            </Box>
        </Box>
    );
};

interface DeviceCardProps {
    device: DeviceInfo;
    fallbackName: string;
}

const DeviceCard: React.FC<DeviceCardProps> = ({ device, fallbackName }) => {
    const [counters, setCounters] = useState<CountersResponse | null>(null);
    const [loading, setLoading] = useState(true);

    const deviceName = device.name || fallbackName;

    useEffect(() => {
        const fetchCounters = async () => {
            try {
                const response = await API.counters.device({ device: deviceName });
                setCounters(response);
            } catch (error) {
                console.error('Failed to fetch device counters:', error);
            } finally {
                setLoading(false);
            }
        };
        fetchCounters();
    }, [deviceName]);

    const displayName = device.type
        ? `${device.type}:${deviceName}`
        : deviceName;

    const inputPipelines = device.inputPipelines ?? [];
    const outputPipelines = device.outputPipelines ?? [];
    const hasPipelines = inputPipelines.length > 0 || outputPipelines.length > 0;

    return (
        <Box className="device-card">
            <Box className="device-card__header">
                <Text variant="body-1" className="device-card__title">
                    {displayName}
                </Text>
            </Box>
            <Box className="device-card__body">
                <CountersDisplay counters={counters} loading={loading} />
                {hasPipelines ? (
                    <>
                        <PipelineGroup label="Input Pipelines" pipelines={inputPipelines} />
                        <PipelineGroup label="Output Pipelines" pipelines={outputPipelines} />
                    </>
                ) : (
                    <Text variant="body-2" className="device-card__no-pipelines">
                        No pipelines
                    </Text>
                )}
            </Box>
        </Box>
    );
};

export const DevicesSection: React.FC<DevicesSectionProps> = ({ instance }) => {
    const devices = instance.devices ?? [];

    return (
        <Box className="inspect-section-box">
            <Text variant="header-1" className="inspect-section__header">
                Devices
            </Text>
            {devices.length > 0 ? (
                <Box className="devices-section__grid">
                    {devices.map((device, idx) => (
                        <DeviceCard
                            key={device.name ?? idx}
                            device={device}
                            fallbackName={`Device ${idx}`}
                        />
                    ))}
                </Box>
            ) : (
                <Text variant="body-1" color="secondary" className="inspect-text--block">
                    No devices
                </Text>
            )}
        </Box>
    );
};
