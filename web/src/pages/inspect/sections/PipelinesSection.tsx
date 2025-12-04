import React from 'react';
import { Box, Text } from '@gravity-ui/uikit';
import type { InstanceInfo } from '../../../api/inspect';

export interface PipelinesSectionProps {
    instance: InstanceInfo;
}

export const PipelinesSection: React.FC<PipelinesSectionProps> = ({ instance }) => {
    return (
        <Box style={{ marginBottom: '24px' }}>
            <Text variant="header-1" style={{ marginBottom: '12px' }}>
                Pipelines
            </Text>
            {instance.pipelines && instance.pipelines.length > 0 ? (
                <Box>
                    {instance.pipelines.map((pipeline, idx) => (
                        <Box key={idx} style={{ marginBottom: '16px' }}>
                            <Text variant="body-1" style={{ marginBottom: '8px', fontWeight: 'bold' }}>
                                Pipeline {pipeline.name}
                            </Text>
                            <Box style={{ marginLeft: '16px' }}>
                                <Text variant="body-1" style={{ marginBottom: '4px' }}>rx</Text>
                                {pipeline.functions?.map((funcName, funcIdx) => (
                                    <Text key={funcIdx} variant="body-1" style={{ marginLeft: '16px', marginBottom: '4px' }}>
                                        {funcName}
                                    </Text>
                                ))}
                                <Text variant="body-1" style={{ marginTop: '4px' }}>tx</Text>
                            </Box>
                        </Box>
                    ))}
                </Box>
            ) : (
                <Text variant="body-1" color="secondary" style={{ display: 'block' }}>No pipelines</Text>
            )}
        </Box>
    );
};

