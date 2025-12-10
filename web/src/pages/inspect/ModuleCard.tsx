import React from 'react';
import { Box, Text } from '@gravity-ui/uikit';
import { MODULE_DESCRIPTIONS } from './constants';
import { formatModuleName } from './utils';
import './inspect.css';

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
        <Box className="module-card">
            <Box className="module-card__content">
                <Box className="module-card__header">
                    <Text variant="subheader-1" style={{ fontWeight: '500' }}>
                        {module.name ? formatModuleName(module.name) : `Module ${idx}`}
                    </Text>
                    {module.name && MODULE_DESCRIPTIONS[module.name.toLowerCase()] && (
                        <Text variant="body-1" color="secondary">
                            {MODULE_DESCRIPTIONS[module.name.toLowerCase()]}
                        </Text>
                    )}
                </Box>
                <Box className="module-card__row">
                    <Text variant="body-1" color="secondary">ID</Text>
                    <Box className="module-card__separator" />
                    <Text variant="body-1" style={{ fontWeight: 'bold' }}>{idx}</Text>
                </Box>
                <Box className="module-card__row">
                    <Text variant="body-1" color="secondary">Configs</Text>
                    <Box className="module-card__separator" />
                    <Text variant="body-1" style={{ fontWeight: 'bold' }}>{configCount}</Text>
                </Box>
                <Box className="module-card__row">
                    <Text variant="body-1" color="secondary">Pipelines</Text>
                    <Box className="module-card__separator" />
                    <Text variant="body-1" style={{ fontWeight: 'bold' }}>{pipelineUsage}</Text>
                </Box>
            </Box>
        </Box>
    );
};
