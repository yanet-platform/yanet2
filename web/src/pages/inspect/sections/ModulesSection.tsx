import React from 'react';
import { Box, Text } from '@gravity-ui/uikit';
import type { InstanceInfo } from '../../../api/inspect';
import { ModuleCard } from '../ModuleCard';

export interface ModulesSectionProps {
    instance: InstanceInfo;
}

export const ModulesSection: React.FC<ModulesSectionProps> = ({ instance }) => {
    return (
        <Box style={{ marginBottom: '24px' }}>
            <Text variant="header-1" style={{ marginBottom: '12px' }}>
                Dataplane Modules
            </Text>
            {instance.dpModules && instance.dpModules.length > 0 ? (
                <Box style={{ padding: '12px' }}>
                    <Box style={{ display: 'flex', flexWrap: 'wrap', gap: '12px', alignItems: 'stretch' }}>
                        {instance.dpModules.map((module, idx) => {
                            const configCount = instance.cpConfigs?.filter(
                                (cfg) => cfg.type?.toLowerCase() === module.name?.toLowerCase()
                            ).length || 0;

                            const pipelineUsage = instance.pipelines?.reduce((count, pipeline) => {
                                const usesModule = pipeline.functions?.some((funcName) => {
                                    const func = instance.functions?.find(f => f.Name === funcName);
                                    return func?.chains?.some(chain =>
                                        chain.modules?.some(mod =>
                                            mod.type?.toLowerCase() === module.name?.toLowerCase()
                                        )
                                    );
                                });
                                return count + (usesModule ? 1 : 0);
                            }, 0) || 0;

                            return (
                                <ModuleCard
                                    key={idx}
                                    module={module}
                                    idx={idx}
                                    configCount={configCount}
                                    pipelineUsage={pipelineUsage}
                                />
                            );
                        })}
                    </Box>
                </Box>
            ) : (
                <Text variant="body-1" color="secondary" style={{ display: 'block' }}>No modules</Text>
            )}
        </Box>
    );
};

