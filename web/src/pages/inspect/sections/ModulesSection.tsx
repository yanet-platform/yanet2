import React from 'react';
import { Box, Text } from '@gravity-ui/uikit';
import { LayoutCellsLarge } from '@gravity-ui/icons';
import type { InstanceInfo } from '../../../api/inspect';
import { InspectSection } from '../InspectSection';
import { ModuleCard } from '../ModuleCard';
import '../inspect.scss';

export interface ModulesSectionProps {
    instance: InstanceInfo;
}

export const ModulesSection: React.FC<ModulesSectionProps> = ({ instance }) => {
    const modules = instance.dp_modules ?? [];

    return (
        <InspectSection
            title="Dataplane Modules"
            icon={LayoutCellsLarge}
            count={modules.length}
            variant="modules"
            collapsible
            defaultExpanded
        >
            {modules.length > 0 ? (
                <Box className="modules-grid">
                    {modules.map((module) => {
                        const configCount = instance.cp_configs?.filter(
                            (cfg) => cfg.type?.toLowerCase() === module.name?.toLowerCase()
                        ).length || 0;

                        const pipelineUsage = instance.pipelines?.reduce((count, pipeline) => {
                            const usesModule = pipeline.functions?.some((funcName) => {
                                const func = instance.functions?.find(f => f.name === funcName);
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
                <Text variant="body-1" color="secondary" className="inspect-text--block">
                    No modules
                </Text>
            )}
        </InspectSection>
    );
};
