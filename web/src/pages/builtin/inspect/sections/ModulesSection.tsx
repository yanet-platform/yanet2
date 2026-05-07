import React, { useMemo } from 'react';
import type { InstanceInfo } from '../../../../api/inspect';
import { InspectCard } from '../InspectCard';
import { ModuleCard } from '../ModuleCard';
import { EmptyState } from '../../../../components';

export interface ModulesSectionProps {
    instance: InstanceInfo;
}

const ModulesLegend: React.FC = () => (
    <span className="inspect-legend">
        <span>
            <span className="inspect-legend-dot" style={{ background: 'var(--inspect-ok)' }} />
            in use
        </span>
        <span>
            <span className="inspect-legend-dot" style={{ background: 'var(--inspect-idle)' }} />
            available
        </span>
    </span>
);

export const ModulesSection: React.FC<ModulesSectionProps> = ({ instance }) => {
    const modules = instance.dp_modules ?? [];

    const { configCountByModule, pipelineCountByModule } = useMemo(() => {
        const configCount = new Map<string, number>();
        for (const cfg of instance.cp_configs ?? []) {
            const t = cfg.type?.toLowerCase();
            if (!t) {
                continue;
            }
            configCount.set(t, (configCount.get(t) ?? 0) + 1);
        }

        const pipelineUses = new Map<string, Set<string>>();
        const funcByName = new Map(
            (instance.functions ?? []).map((f) => [f.name ?? '', f]),
        );
        for (const pipe of instance.pipelines ?? []) {
            const pipeName = pipe.name ?? '';
            for (const fname of pipe.functions ?? []) {
                const fn = funcByName.get(fname);
                for (const ch of fn?.chains ?? []) {
                    for (const mod of ch.modules ?? []) {
                        const t = mod.type?.toLowerCase();
                        if (!t) {
                            continue;
                        }
                        let s = pipelineUses.get(t);
                        if (!s) {
                            s = new Set<string>();
                            pipelineUses.set(t, s);
                        }
                        s.add(pipeName);
                    }
                }
            }
        }

        const pipelineCount = new Map<string, number>();
        for (const [t, set] of pipelineUses) {
            pipelineCount.set(t, set.size);
        }

        return { configCountByModule: configCount, pipelineCountByModule: pipelineCount };
    }, [instance]);

    return (
        <InspectCard title="Dataplane modules" count={modules.length} right={<ModulesLegend />}>
            {modules.length > 0 ? (
                <div className="inspect-mod-grid">
                    {modules.map((module) => {
                        const t = module.name?.toLowerCase() ?? '';
                        return (
                            <ModuleCard
                                key={module.name}
                                module={module}
                                configCount={configCountByModule.get(t) ?? 0}
                                pipelineUsage={pipelineCountByModule.get(t) ?? 0}
                            />
                        );
                    })}
                </div>
            ) : (
                <EmptyState message="No modules" compact />
            )}
        </InspectCard>
    );
};
