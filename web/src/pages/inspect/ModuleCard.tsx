import React from 'react';
import { Box, Text } from '@gravity-ui/uikit';
import { MODULE_DESCRIPTIONS } from './constants';
import { formatModuleName } from './utils';

export interface ModuleCardProps {
    module: { name?: string };
    idx: number;
    configCount: number;
    pipelineUsage: number;
}

export const ModuleCard: React.FC<ModuleCardProps> = ({
    module,
    idx,
    configCount,
    pipelineUsage,
}) => {
    return (
        <Box
            style={{
                border: '1px solid var(--g-color-line-generic)',
                borderRadius: '8px',
                padding: '12px 16px',
                backgroundColor: 'var(--g-color-base-float)',
                display: 'flex',
                flexDirection: 'column',
                width: '200px',
                transition: 'all 0.2s ease',
            }}
            onMouseEnter={(e) => {
                const target = e.currentTarget as unknown as HTMLElement;
                if (target) {
                    target.style.borderColor = 'var(--g-color-line-brand)';
                    target.style.backgroundColor = 'var(--g-color-base-simple-hover)';
                }
            }}
            onMouseLeave={(e) => {
                const target = e.currentTarget as unknown as HTMLElement;
                if (target) {
                    target.style.borderColor = 'var(--g-color-line-generic)';
                    target.style.backgroundColor = 'var(--g-color-base-float)';
                }
            }}
        >
            <Box style={{ display: 'flex', flexDirection: 'column', gap: '8px', flex: '1', minHeight: 0 }}>
                <Box style={{ minHeight: '60px', display: 'flex', flexDirection: 'column', gap: '8px' }}>
                    <Text variant="subheader-1" style={{ fontWeight: '500' }}>
                        {module.name ? formatModuleName(module.name) : `Module ${idx}`}
                    </Text>
                    {module.name && MODULE_DESCRIPTIONS[module.name.toLowerCase()] && (
                        <Text variant="body-1" color="secondary">
                            {MODULE_DESCRIPTIONS[module.name.toLowerCase()]}
                        </Text>
                    )}
                </Box>
                <Box style={{ display: 'flex', alignItems: 'flex-end', justifyContent: 'space-between', width: '100%' }}>
                    <Text variant="body-1" color="secondary">ID</Text>
                    <Box style={{ flex: '1', borderBottom: '1px dotted var(--g-color-line-generic)', margin: '0 8px 2px 8px', height: '1px' }} />
                    <Text variant="body-1" style={{ fontWeight: 'bold' }}>{idx}</Text>
                </Box>
                <Box style={{ display: 'flex', alignItems: 'flex-end', justifyContent: 'space-between', width: '100%' }}>
                    <Text variant="body-1" color="secondary">Configs</Text>
                    <Box style={{ flex: '1', borderBottom: '1px dotted var(--g-color-line-generic)', margin: '0 8px 2px 8px', height: '1px' }} />
                    <Text variant="body-1" style={{ fontWeight: 'bold' }}>{configCount}</Text>
                </Box>
                <Box style={{ display: 'flex', alignItems: 'flex-end', justifyContent: 'space-between', width: '100%' }}>
                    <Text variant="body-1" color="secondary">Pipelines</Text>
                    <Box style={{ flex: '1', borderBottom: '1px dotted var(--g-color-line-generic)', margin: '0 8px 2px 8px', height: '1px' }} />
                    <Text variant="body-1" style={{ fontWeight: 'bold' }}>{pipelineUsage}</Text>
                </Box>
            </Box>
        </Box>
    );
};

