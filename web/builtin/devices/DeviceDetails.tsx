import React, { useState, useCallback, useEffect, useMemo, lazy, Suspense } from 'react';
import {
    IconHdd,
    IconArrowDown,
    IconArrowUp,
    IconSave,
} from './components/Icons';
import { DeviceDiffModal } from './components/DeviceDiffModal';
import { BigSpark } from './components/BigSpark';
import { formatBps, formatPps } from '@yanet/core/utils';
import { PipelineTable } from './PipelineTable';
import { deviceTypeManifest } from '@yanet/core/registry';
import type { LocalDevice } from './types';
import type { PipelineId } from '@yanet/core/api/pipelines';
import type { DevicePipeline } from '@yanet/core/api/devices';
import type { DeviceCounterData } from '@yanet/core/hooks/useDeviceCounters';
import type { CounterHistoryEntry } from '@yanet/core/hooks/useCounterHistory';

export interface DeviceDetailsProps {
    device: LocalDevice | null;
    loadPipelineList: () => Promise<PipelineId[]>;
    loadDeviceExt: (device: LocalDevice) => Promise<void>;
    counterData: DeviceCounterData | undefined;
    history: CounterHistoryEntry | undefined;
    onUpdate: (updates: Partial<LocalDevice>) => void;
    onSave: () => Promise<boolean>;
    getServerDevice: (name: string) => LocalDevice | null;
}

interface MetricBlockProps {
    subLabel: string;
    isRx: boolean;
    isPps: boolean;
    value: number;
    deviceId: string;
    series: string;
    color: string;
    history: number[];
}

const MetricBlock = ({
    subLabel,
    isRx,
    isPps,
    value,
    deviceId,
    series,
    color,
    history,
}: MetricBlockProps): React.JSX.Element => (
    <div className="dv-metric">
        <div className="dv-metric-hd">
            <span className="dv-metric-dir">
                {isRx ? <IconArrowDown /> : <IconArrowUp />}
                <span style={{ color }}>{isRx ? 'RX' : 'TX'}</span>
            </span>
            <span className="dv-metric-lbl">{subLabel}</span>
        </div>
        <div className="dv-metric-val mono">{isPps ? formatPps(value) : formatBps(value)}</div>
        <BigSpark
            deviceId={deviceId}
            series={series}
            values={history}
            color={color}
            height={48}
        />
    </div>
);

interface PropCellProps {
    label: string;
    value: string;
    mono?: boolean;
}

const PropCell = ({ label, value, mono = false }: PropCellProps): React.JSX.Element => (
    <div className="dv-prop">
        <div className="dv-prop-lbl">{label}</div>
        <div className={"dv-prop-val" + (mono ? ' mono' : '')}>{value}</div>
    </div>
);

interface DeviceMetricsProps {
    deviceId: string;
    counterData: DeviceCounterData | undefined;
    history: CounterHistoryEntry | undefined;
}

// Live RX/TX metric grid for the selected device.
//
// This is the only part of the detail panel that consumes interpolated
// counters, which refresh on every animation frame. Keeping it in its own
// component lets the compiler skip re-rendering the heavier config sections
// (DeviceBody) when only the counters tick.
const DeviceMetrics = ({ deviceId, counterData, history }: DeviceMetricsProps): React.JSX.Element => {
    const rxPps = counterData?.rx.pps ?? 0;
    const rxBps = counterData?.rx.bps ?? 0;
    const txPps = counterData?.tx.pps ?? 0;
    const txBps = counterData?.tx.bps ?? 0;

    const rxPpsHistory = history?.rx ?? [];
    const txPpsHistory = history?.tx ?? [];
    const rxBpsHistory = history?.rxBytes ?? [];
    const txBpsHistory = history?.txBytes ?? [];

    return (
        <div className="dv-metric-grid">
            <MetricBlock
                subLabel="packets / sec"
                isRx={true}
                isPps={true}
                value={rxPps}
                deviceId={deviceId}
                series="rx-pps"
                color="var(--teal)"
                history={rxPpsHistory}
            />
            <MetricBlock
                subLabel="bytes / sec"
                isRx={true}
                isPps={false}
                value={rxBps}
                deviceId={deviceId}
                series="rx-bps"
                color="var(--teal)"
                history={rxBpsHistory}
            />
            <MetricBlock
                subLabel="packets / sec"
                isRx={false}
                isPps={true}
                value={txPps}
                deviceId={deviceId}
                series="tx-pps"
                color="var(--blue)"
                history={txPpsHistory}
            />
            <MetricBlock
                subLabel="bytes / sec"
                isRx={false}
                isPps={false}
                value={txBps}
                deviceId={deviceId}
                series="tx-bps"
                color="var(--blue)"
                history={txBpsHistory}
            />
        </div>
    );
};

interface DeviceBodyProps {
    device: LocalDevice;
    availablePipelines: PipelineId[];
    loadingPipelines: boolean;
    onUpdate: (updates: Partial<LocalDevice>) => void;
}

// Static config sections of the detail panel: counters, properties, pipelines,
// and the type-specific detail extension.
//
// None of these depend on the live counters, so the compiler keeps this subtree
// mounted across counter ticks and only re-renders it on an actual edit.
const DeviceBody = ({
    device,
    availablePipelines,
    loadingPipelines,
    onUpdate,
}: DeviceBodyProps): React.JSX.Element => {
    const manifest = deviceTypeManifest(device.type);
    const extraRows = manifest?.propertyRows?.(device) ?? [];

    const Detail = useMemo(() => {
        const loader = deviceTypeManifest(device.type)?.loadDetail;
        return loader ? lazy(loader) : null;
    }, [device.type]);

    const handleInputPipelinesChange = useCallback((pipelines: DevicePipeline[]) => {
        onUpdate({ inputPipelines: pipelines });
    }, [onUpdate]);

    const handleOutputPipelinesChange = useCallback((pipelines: DevicePipeline[]) => {
        onUpdate({ outputPipelines: pipelines });
    }, [onUpdate]);

    const handleUpdateExt = useCallback((patch: Record<string, unknown>) => {
        const current = (device.ext[device.type] as Record<string, unknown> | undefined) ?? {};
        onUpdate({ ext: { ...device.ext, [device.type]: { ...current, ...patch } } });
    }, [onUpdate, device.ext, device.type]);

    return (
        <>
            <div className="dv-section">
                <div className="dv-section-hd"><span>Counters</span></div>
                <div className="dv-err-strip">
                    <div className="dv-err-chip">
                        <span className="dv-err-chip-lbl">Errors</span>
                        <span className="dv-err-chip-val mono">0</span>
                    </div>
                    <div className="dv-err-chip">
                        <span className="dv-err-chip-lbl">Drops</span>
                        <span className="dv-err-chip-val mono">0</span>
                    </div>
                    <div className="dv-err-chip">
                        <span className="dv-err-chip-lbl">Discards</span>
                        <span className="dv-err-chip-val mono">0</span>
                    </div>
                </div>
            </div>

            <div className="dv-section">
                <div className="dv-section-hd"><span>Properties</span></div>
                <div className="dv-prop-grid">
                    <PropCell label="MAC address" value="—" mono />
                    <PropCell label="MTU" value="—" mono />
                    {extraRows.map(row => (
                        <PropCell key={row.label} label={row.label} value={row.value} mono={row.mono} />
                    ))}
                    <PropCell label="NUMA node" value="—" mono />
                    <PropCell label="Type" value={manifest?.typeDescription ?? device.type} />
                </div>
            </div>

            <div className="dv-section">
                <div className="dv-section-hd"><span>Pipelines</span></div>
                <div className="dv-pipe-cols">
                    <PipelineTable
                        pipelineLabel="RX Pipeline"
                        pipelines={device.inputPipelines}
                        availablePipelines={availablePipelines}
                        loadingPipelines={loadingPipelines}
                        color="var(--teal)"
                        onChange={handleInputPipelinesChange}
                    />
                    <PipelineTable
                        pipelineLabel="TX Pipeline"
                        pipelines={device.outputPipelines}
                        availablePipelines={availablePipelines}
                        loadingPipelines={loadingPipelines}
                        color="var(--blue)"
                        onChange={handleOutputPipelinesChange}
                    />
                </div>
            </div>

            {Detail && (
                <Suspense fallback={null}>
                    <Detail
                        device={device}
                        ext={device.ext[device.type]}
                        onUpdateExt={handleUpdateExt}
                    />
                </Suspense>
            )}
        </>
    );
};

export const DeviceDetails: React.FC<DeviceDetailsProps> = ({
    device,
    loadPipelineList,
    loadDeviceExt,
    counterData,
    history,
    onUpdate,
    onSave,
    getServerDevice,
}) => {
    const [saving, setSaving] = useState(false);
    const [diffOpen, setDiffOpen] = useState(false);
    const [availablePipelines, setAvailablePipelines] = useState<PipelineId[]>([]);
    const [loadingPipelines, setLoadingPipelines] = useState(false);

    useEffect(() => {
        if (!device) return;
        const load = async () => {
            setLoadingPipelines(true);
            const pipelines = await loadPipelineList();
            setAvailablePipelines(pipelines);
            setLoadingPipelines(false);
        };
        load();
    }, [device, loadPipelineList]);

    // Lazily hydrate the device's type-specific ext the first time it opens.
    useEffect(() => {
        if (device && !device.loaded) {
            loadDeviceExt(device);
        }
    }, [device, loadDeviceExt]);

    const handleSaveClick = useCallback(async () => {
        // A type that opts out of the diff modal commits whatever changed
        // directly (e.g. rate or uploaded frames that are not part of the YAML).
        if (device && deviceTypeManifest(device.type)?.confirmViaDiff === false) {
            setSaving(true);
            try {
                await onSave();
            } finally {
                setSaving(false);
            }
            return;
        }
        setDiffOpen(true);
    }, [device, onSave]);

    const handleDiffApply = useCallback(async (): Promise<void> => {
        setSaving(true);
        try {
            const ok = await onSave();
            if (!ok) {
                throw new Error('Save failed');
            }
        } finally {
            setSaving(false);
        }
    }, [onSave]);

    const handleDiffClose = useCallback(() => {
        setDiffOpen(false);
    }, []);

    if (!device) {
        return (
            <div className="dv-detail dv-detail-empty">
                <div className="dv-detail-empty-inner">
                    <div className="dv-detail-empty-icon">
                        <IconHdd size={32} />
                    </div>
                    <div className="dv-detail-empty-title">No device selected</div>
                    <div className="dv-detail-empty-sub">
                        Pick a device from the list to see its metrics, configuration and attached pipelines.
                    </div>
                </div>
            </div>
        );
    }

    const manifest = deviceTypeManifest(device.type);
    const Icon = manifest?.icon;
    const name = device.id.name || '';
    const iconColor = manifest?.accentColor ?? 'var(--teal)';
    const kindTag = manifest?.kindTag(device) ?? device.type.toUpperCase();
    const canSave = (device.isDirty || device.isNew) && !saving;
    const serverDevice = name ? getServerDevice(name) : null;

    return (
        <div className="dv-detail">
            <div className="dv-detail-hd">
                <div className="dv-detail-hd-left">
                    <span className="dv-detail-icon" style={{ color: iconColor }}>
                        {Icon && <Icon size={20} />}
                    </span>
                    <div className="dv-detail-title-wrap">
                        <div className="dv-detail-title">
                            <span className="dv-detail-name">{name}</span>
                            <span className={"dv-kind-tag kind-" + device.type}>
                                {kindTag}
                            </span>
                            {(device.isDirty || device.isNew) && (
                                <span className="dv-unsaved">unsaved changes</span>
                            )}
                        </div>
                        <div className="dv-detail-sub">
                            <span className="dv-link-pill link-unknown">
                                <span className="dv-link-pill-dot" />
                                Link unknown
                            </span>
                            <span className="dv-meta-sep">·</span>
                            <span>— · —</span>
                            <span className="dv-meta-sep">·</span>
                            <span>NUMA —</span>
                        </div>
                    </div>
                </div>
                <div className="dv-detail-hd-actions">
                    <button
                        className={"btn-primary" + (canSave ? '' : ' btn-primary-dim')}
                        onClick={handleSaveClick}
                        disabled={!canSave}
                    >
                        <IconSave size={13} />
                        {saving ? 'Saving...' : 'Save'}
                    </button>
                </div>
            </div>

            <div className="dv-detail-scroll">
                <DeviceMetrics deviceId={name} counterData={counterData} history={history} />
                <DeviceBody
                    device={device}
                    availablePipelines={availablePipelines}
                    loadingPipelines={loadingPipelines}
                    onUpdate={onUpdate}
                />
            </div>

            {diffOpen && (
                <DeviceDiffModal
                    device={device}
                    serverDevice={serverDevice}
                    onClose={handleDiffClose}
                    onApply={handleDiffApply}
                />
            )}
        </div>
    );
};
