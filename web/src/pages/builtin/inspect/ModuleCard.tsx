import React from 'react';
import { MODULE_DESCRIPTIONS } from './constants';
import { formatModuleName } from './utils';
import type { DPModuleInfo } from '../../../api';

export interface ModuleCardProps {
    module: DPModuleInfo;
    configCount: number;
    pipelineUsage: number;
}

export const ModuleCard: React.FC<ModuleCardProps> = ({
    module,
    configCount,
    pipelineUsage,
}) => {
    const inUse = configCount > 0 || pipelineUsage > 0;
    const cls = inUse ? 'inspect-mod' : 'inspect-mod is-off';
    const desc = module.name ? MODULE_DESCRIPTIONS[module.name.toLowerCase()] ?? '' : '';

    return (
        <div className={cls}>
            <div className="inspect-mod-head">
                <span className="inspect-mod-name">{formatModuleName(module.name ?? '')}</span>
                <span
                    className="inspect-mod-dot"
                    style={{ background: inUse ? 'var(--inspect-ok)' : 'var(--inspect-idle)' }}
                />
            </div>
            <div className="inspect-mod-desc">{desc}</div>
            <div className="inspect-mod-meta">
                <span>
                    <b className="inspect-num">{configCount}</b> cfg
                </span>
                <span>
                    <b className="inspect-num">{pipelineUsage}</b> pipe
                </span>
            </div>
        </div>
    );
};
