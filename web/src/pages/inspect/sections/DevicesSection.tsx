import React from 'react';
import { Box, Text } from '@gravity-ui/uikit';
import type { InstanceInfo, DeviceInfo, DevicePipelineInfo } from '../../../api/inspect';
import { formatUint64 } from '../utils';
import '../inspect.css';

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

interface DeviceCardProps {
    device: DeviceInfo;
    fallbackName: string;
}

const DeviceCard: React.FC<DeviceCardProps> = ({ device, fallbackName }) => {
    const displayName = device.type
        ? `${device.type}:${device.name || fallbackName}`
        : device.name || fallbackName;

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
        <Box>
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
                <Text variant="body-1" color="secondary" style={{ display: 'block' }}>
                    No devices
                </Text>
            )}
        </Box>
    );
};
