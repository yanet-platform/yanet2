import React from 'react';
import type { InstanceInfo } from '../../../api/inspect';

export interface KpiStripProps {
    instance: InstanceInfo;
}

/** Compact horizontal strip of device/pipeline/function/module/config counts. */
export const KpiStrip: React.FC<KpiStripProps> = ({ instance }) => {
    const devices = instance.devices ?? [];
    const pipelines = instance.pipelines ?? [];
    const functions = instance.functions ?? [];
    const modules = instance.dp_modules ?? [];
    const configs = instance.cp_configs ?? [];

    return (
        <div className="dash-kpi-counts">
            <div className="dash-kpi-counts__cell">
                <span className="dash-kpi-counts__label">DEVICES</span>
                <span className="dash-kpi-counts__value">{devices.length}</span>
            </div>
            <div className="dash-kpi-counts__cell">
                <span className="dash-kpi-counts__label">PIPELINES</span>
                <span className="dash-kpi-counts__value">{pipelines.length}</span>
            </div>
            <div className="dash-kpi-counts__cell">
                <span className="dash-kpi-counts__label">FUNCTIONS</span>
                <span className="dash-kpi-counts__value">{functions.length}</span>
            </div>
            <div className="dash-kpi-counts__cell">
                <span className="dash-kpi-counts__label">MODULES</span>
                <span className="dash-kpi-counts__value">{modules.length}</span>
            </div>
            <div className="dash-kpi-counts__cell">
                <span className="dash-kpi-counts__label">CONFIGS</span>
                <span className="dash-kpi-counts__value">{configs.length}</span>
            </div>
        </div>
    );
};
