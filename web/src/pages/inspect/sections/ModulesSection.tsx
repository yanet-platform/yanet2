import React from 'react';
import { Box, Text } from '@gravity-ui/uikit';
import type { InstanceInfo } from '../../../api/inspect';
import { ModuleCard } from '../ModuleCard';
import '../inspect.css';

export interface ModulesSectionProps {
    instance: InstanceInfo;
}

export const ModulesSection: React.FC<ModulesSectionProps> = ({ instance }) => {
    return (
        <Box className="inspect-section-box">
            <Text variant="header-1" className="inspect-section__header">
                Dataplane Modules
            </Text>
            {instance.dpModules && instance.dpModules.length > 0 ? (
                <Box className="inspect-section__modules-grid">
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
                                key={module.name}
                                module={module}
                                configCount={configCount}
                                pipelineUsage={pipelineUsage}
                            />
                        );
                    })}
                </Box>
            ) : (
                <Text variant="body-1" color="secondary" style={{ display: 'block' }}>No modules</Text>
            )}
        </Box>
    );
};
