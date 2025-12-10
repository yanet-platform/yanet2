import React from 'react';
import { Box, Text } from '@gravity-ui/uikit';
import type { InstanceInfo } from '../../../api/inspect';
import { formatUint64 } from '../utils';
import '../inspect.css';

export interface DevicesSectionProps {
    instance: InstanceInfo;
}

export const DevicesSection: React.FC<DevicesSectionProps> = ({ instance }) => {
    return (
        <Box>
            <Text variant="header-1" className="inspect-section__header">
                Devices
            </Text>
            {instance.devices && instance.devices.length > 0 ? (
                <Box>
                    {instance.devices.map((device, deviceIdx) => (
                        <Box key={deviceIdx} className="devices-section__device">
                            <Text variant="body-1" className="devices-section__device-name">
                                {device.type ? `${device.type}:` : ''}{device.name || `Device ${deviceIdx}`}
                            </Text>
                            {(device.inputPipelines && device.inputPipelines.length > 0) && (
                                <Box className="devices-section__pipeline-group">
                                    <Text variant="body-1" color="secondary" className="devices-section__pipeline-label">
                                        Input Pipelines:
                                    </Text>
                                    {device.inputPipelines.map((devicePipeline, pipelineIdx) => (
                                        <Text key={pipelineIdx} variant="body-1" className="devices-section__pipeline-item">
                                            {devicePipeline.name} (weight: {formatUint64(devicePipeline.weight)})
                                        </Text>
                                    ))}
                                </Box>
                            )}
                            {(device.outputPipelines && device.outputPipelines.length > 0) && (
                                <Box className="devices-section__pipeline-group">
                                    <Text variant="body-1" color="secondary" className="devices-section__pipeline-label">
                                        Output Pipelines:
                                    </Text>
                                    {device.outputPipelines.map((devicePipeline, pipelineIdx) => (
                                        <Text key={pipelineIdx} variant="body-1" className="devices-section__pipeline-item">
                                            {devicePipeline.name} (weight: {formatUint64(devicePipeline.weight)})
                                        </Text>
                                    ))}
                                </Box>
                            )}
                            {(!device.inputPipelines || device.inputPipelines.length === 0) &&
                             (!device.outputPipelines || device.outputPipelines.length === 0) && (
                                <Text variant="body-1" color="secondary" className="devices-section__no-pipelines">
                                    No pipelines
                                </Text>
                            )}
                        </Box>
                    ))}
                </Box>
            ) : (
                <Text variant="body-1" color="secondary" style={{ display: 'block' }}>No devices</Text>
            )}
        </Box>
    );
};
