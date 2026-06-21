import { useMemo } from 'react';
import type { InstanceInfo } from '@yanet/core/api/inspect';
import {
    computeModulePipelineUsage,
    getModuleDescription,
    normalizeModuleName,
} from './utils';

export interface ModuleCardData {
    key: string;
    name: string;
    cfg: number;
    pipe: number;
    inUse: boolean;
    desc: string;
}

/** Derives the per-module card data array from an instance snapshot. */
export const useModuleCards = (instance: InstanceInfo): ModuleCardData[] => {
    const modules = instance.dp_modules ?? [];
    const configs = instance.cp_configs ?? [];

    const pipeUsage = useMemo(() => computeModulePipelineUsage(instance), [instance]);

    const moduleData = useMemo(
        () =>
            modules.map((m, idx) => {
                const name = m.name ?? '';
                const key = name || `module-${idx}`;
                const moduleKey = normalizeModuleName(name);
                const cfg = configs.filter(
                    (c) => normalizeModuleName(c.type ?? '') === moduleKey,
                ).length;
                const pipe = pipeUsage.get(moduleKey) ?? 0;
                const inUse = cfg > 0 || pipe > 0;
                return { key, name, cfg, pipe, inUse, desc: getModuleDescription(name) };
            }),
        [modules, configs, pipeUsage],
    );

    return moduleData;
};
