import React, { useMemo } from 'react';
import type { InstanceInfo } from '../../../api/inspect';
import type { DeviceCounterData, DeviceAbsoluteData } from '../../../hooks';
import { KpiCell } from './KpiCell';
import { fmtPkts } from './formatters';

export interface KpiBarProps {
    instance: InstanceInfo;
    deviceCounters: Map<string, DeviceCounterData>;
    deviceAbsolute: Map<string, DeviceAbsoluteData>;
    throughputPps: number;
    throughputSeries: number[];
}

export const KpiBar: React.FC<KpiBarProps> = ({
    instance,
    deviceAbsolute,
    throughputPps,
    throughputSeries,
}) => {
    const devices = instance.devices ?? [];
    const pipelines = instance.pipelines ?? [];
    const functions = instance.functions ?? [];
    const modules = instance.dp_modules ?? [];
    const configs = instance.cp_configs ?? [];

    const { devicesActive, devicesIdle } = useMemo(() => {
        let active = 0;
        for (const d of devices) {
            const name = d.name ?? '';
            const abs = deviceAbsolute.get(name);
            if (abs && (abs.rx.packets > 0 || abs.tx.packets > 0)) {
                active += 1;
            }
        }
        return {
            devicesActive: active,
            devicesIdle: Math.max(0, devices.length - active),
        };
    }, [devices, deviceAbsolute]);

    const pipelinesActive = pipelines.filter((p) => (p.functions?.length ?? 0) > 0).length;
    const pipelinesIdle = Math.max(0, pipelines.length - pipelinesActive);

    const modulesInUse = useMemo(() => {
        const used = new Set<string>();
        for (const cfg of configs) {
            const t = cfg.type?.toLowerCase();
            if (t) used.add(t);
        }
        const funcByName = new Map((functions).map((f) => [f.name ?? '', f]));
        for (const pipe of pipelines) {
            for (const fname of pipe.functions ?? []) {
                const fn = funcByName.get(fname);
                for (const ch of fn?.chains ?? []) {
                    for (const m of ch.modules ?? []) {
                        const t = m.type?.toLowerCase();
                        if (t) used.add(t);
                    }
                }
            }
        }
        let count = 0;
        for (const m of modules) {
            const t = m.name?.toLowerCase() ?? '';
            if (used.has(t)) count += 1;
        }
        return count;
    }, [modules, configs, pipelines, functions]);

    const throughputMinM = ((throughputPps * 60) / 1e6).toFixed(1);

    return (
        <div className="inspect-kpi-bar">
            <KpiCell
                label="Throughput"
                value={`${fmtPkts(throughputPps)} pps`}
                hint={`${throughputMinM}M / min`}
                series={throughputSeries}
                color="var(--inspect-ok)"
                emphasize
            />
            <KpiCell
                label="Devices"
                value={devices.length}
                hint={`${devicesActive} active · ${devicesIdle} idle`}
            />
            <KpiCell
                label="Pipelines"
                value={pipelines.length}
                hint={`${pipelinesActive} active · ${pipelinesIdle} idle`}
            />
            <KpiCell
                label="Functions"
                value={functions.length}
                hint="all healthy"
            />
            <KpiCell
                label="Modules"
                value={modules.length}
                hint={`${modulesInUse} in use`}
            />
            <KpiCell
                label="Configs"
                value={configs.length}
                hint="generation —"
            />
        </div>
    );
};
