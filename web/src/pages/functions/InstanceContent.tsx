import React from 'react';
import { Box, Text } from '@gravity-ui/uikit';
import { PageLoader } from '../../components';
import type { Function as NetworkFunction } from '../../api/functions';
import { FunctionGraph } from './FunctionGraph';

export interface InstanceContentProps {
    instance: number;
    functions: NetworkFunction[];
    loading: boolean;
    onRefresh: () => void;
}

export const InstanceContent: React.FC<InstanceContentProps> = ({
    instance,
    functions,
    loading,
    onRefresh,
}) => {
    if (loading) {
        return <PageLoader loading={loading} size="l" />;
    }

    return (
        <Box style={{ padding: '20px' }}>
            {functions.length === 0 ? (
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
                        No functions configured
                    </Text>
                    <Text variant="body-1" color="secondary">
                        Functions define packet processing chains for pipelines. Use the button in the header to create one.
                    </Text>
                </Box>
            ) : (
                <Box>
                    {functions.map((func, index) => (
                        <FunctionGraph
                            key={func.id?.name || index}
                            functionData={func}
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

