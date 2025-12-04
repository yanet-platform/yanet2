import React from 'react';
import { Box, Text } from '@gravity-ui/uikit';
import { PageLoader } from '../../components';
import type { Pipeline } from '../../api/pipelines';
import { PipelineGraph } from './PipelineGraph';

export interface InstanceContentProps {
    instance: number;
    pipelines: Pipeline[];
    loading: boolean;
    onRefresh: () => void;
}

export const InstanceContent: React.FC<InstanceContentProps> = ({
    instance,
    pipelines,
    loading,
    onRefresh,
}) => {
    if (loading) {
        return <PageLoader loading={loading} size="l" />;
    }

    return (
        <Box style={{ padding: '20px' }}>
            {pipelines.length === 0 ? (
                <Box
                    style={{
                        display: 'flex',
                        flexDirection: 'column',
                        alignItems: 'center',
                        justifyContent: 'center',
                        padding: '60px 20px',
                        textAlign: 'center',
                        border: '2px dashed var(--g-color-line-generic)',
                        borderRadius: '12px',
                        backgroundColor: 'var(--g-color-base-simple-hover)',
                    }}
                >
                    <Text variant="header-1" color="secondary" style={{ marginBottom: '12px' }}>
                        No pipelines configured
                    </Text>
                    <Text variant="body-1" color="secondary">
                        Pipelines define packet processing sequences using network functions. Use the button in the header to create one.
                    </Text>
                </Box>
            ) : (
                <Box>
                    {pipelines.map((pipeline, index) => (
                        <PipelineGraph
                            key={pipeline.id?.name || index}
                            pipelineData={pipeline}
                            instance={instance}
                            onDeleted={onRefresh}
                            onSaved={onRefresh}
                        />
                    ))}
                </Box>
            )}
        </Box>
    );
};
