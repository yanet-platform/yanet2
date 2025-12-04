import React from 'react';
import { Box, Text } from '@gravity-ui/uikit';
import type { InstanceInfo } from '../../../api/inspect';
import { formatUint64 } from '../utils';

export interface DevicesSectionProps {
    instance: InstanceInfo;
}

export const DevicesSection: React.FC<DevicesSectionProps> = ({ instance }) => {
    return (
        <Box>
            <Text variant="header-1" style={{ marginBottom: '12px' }}>
                Devices
            </Text>
            {instance.devices && instance.devices.length > 0 ? (
                <Box>
                    {instance.devices.map((device, deviceIdx) => (
                        <Box key={deviceIdx} style={{ marginBottom: '12px' }}>
                            <Text variant="body-1" style={{ marginBottom: '8px', fontWeight: 'bold' }}>
                                {device.type ? `${device.type}:` : ''}{device.name || `Device ${deviceIdx}`}
                            </Text>
                            {(device.inputPipelines && device.inputPipelines.length > 0) && (
                                <Box style={{ marginLeft: '16px', marginBottom: '8px' }}>
                                    <Text variant="body-1" color="secondary" style={{ marginBottom: '4px' }}>
                                        Input Pipelines:
                                    </Text>
                                    {device.inputPipelines.map((devicePipeline, pipelineIdx) => (
                                        <Text key={pipelineIdx} variant="body-1" style={{ marginLeft: '16px', marginBottom: '4px' }}>
                                            {devicePipeline.name} (weight: {formatUint64(devicePipeline.weight)})
                                        </Text>
                                    ))}
                                </Box>
                            )}
                            {(device.outputPipelines && device.outputPipelines.length > 0) && (
                                <Box style={{ marginLeft: '16px' }}>
                                    <Text variant="body-1" color="secondary" style={{ marginBottom: '4px' }}>
                                        Output Pipelines:
                                    </Text>
                                    {device.outputPipelines.map((devicePipeline, pipelineIdx) => (
                                        <Text key={pipelineIdx} variant="body-1" style={{ marginLeft: '16px', marginBottom: '4px' }}>
                                            {devicePipeline.name} (weight: {formatUint64(devicePipeline.weight)})
                                        </Text>
                                    ))}
                                </Box>
                            )}
                            {(!device.inputPipelines || device.inputPipelines.length === 0) &&
                             (!device.outputPipelines || device.outputPipelines.length === 0) && (
                                <Text variant="body-1" color="secondary" style={{ marginLeft: '16px', display: 'block' }}>
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

