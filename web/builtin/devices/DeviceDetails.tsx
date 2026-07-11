import React, { useState, useCallback, useEffect, useMemo, lazy, Suspense } from 'react';
import {
    IconHdd,
    IconArrowDown,
    IconArrowUp,
    IconWarning,
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
    label: string;
    labelClass: string;
    pps: number;
    bps: number;
    deviceId: string;
    series: string;
    color: string;
    history: number[];
    icon: React.ReactNode;
}

const MetricBlock = ({
    label,
    labelClass,
    pps,
    bps,
    deviceId,
    series,
    color,
    history,
    icon,
}: MetricBlockProps): React.JSX.Element => (
    <div className="dv-metric">
        <div className="dv-metric-hd">
            <span className="dv-metric-dir">
                {icon}
                <span className={labelClass} style={{ color }}>{label}</span>
            </span>
            <span className="dv-metric-lbl mono">{formatBps(bps)}</span>
        </div>
        <div className="dv-metric-val mono">{formatPps(pps)}</div>
        <BigSpark
            deviceId={deviceId}
            series={series}
            values={history}
            color={color}
            height={56}
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

// Live metric grid for the selected device, organised by direction.
//
// The dataplane exposes rx/tx/drop counters per input (from NIC) and output
// (toward NIC) handler. This renders two sections — Input and Output — each
// with a throughput (RX), forward (TX) and drop (DROP) card.
//
// This is the only part of the detail panel that consumes interpolated
// counters, which refresh on every animation frame. Keeping it in its own
// component lets the compiler skip re-rendering the heavier config sections
// (DeviceBody) when only the counters tick.
const DeviceMetrics = ({ deviceId, counterData, history }: DeviceMetricsProps): React.JSX.Element => {
    return (
        <div className="dv-metrics">
            <div className="dv-metric-section">
                <div className="dv-metric-section-hd">
                    <span>Input</span>
                    <span className="dv-metric-section-sub">from NIC → pipeline</span>
                </div>
                <div className="dv-metric-grid dv-metric-grid--3">
                    <MetricBlock
                        label="RX"
                        labelClass="dv-lbl-rx"
                        pps={counterData?.inputRx.pps ?? 0}
                        bps={counterData?.inputRx.bps ?? 0}
                        deviceId={deviceId}
                        series="input-rx"
                        color="var(--teal)"
                        history={history?.inputRx ?? []}
                        icon={<IconArrowDown />}
                    />
                    <MetricBlock
                        label="TX"
                        labelClass="dv-lbl-tx"
                        pps={counterData?.inputTx.pps ?? 0}
                        bps={counterData?.inputTx.bps ?? 0}
                        deviceId={deviceId}
                        series="input-tx"
                        color="var(--teal)"
                        history={history?.inputTx ?? []}
                        icon={<IconArrowUp />}
                    />
                    <MetricBlock
                        label="DROP"
                        labelClass="dv-lbl-drop"
                        pps={counterData?.inputDrop.pps ?? 0}
                        bps={counterData?.inputDrop.bps ?? 0}
                        deviceId={deviceId}
                        series="input-drop"
                        color="var(--red)"
                        history={history?.inputDrop ?? []}
                        icon={<IconWarning />}
                    />
                </div>
            </div>
            <div className="dv-metric-section">
                <div className="dv-metric-section-hd">
                    <span>Output</span>
                    <span className="dv-metric-section-sub">from pipeline → NIC</span>
                </div>
                <div className="dv-metric-grid dv-metric-grid--3">
                    <MetricBlock
                        label="RX"
                        labelClass="dv-lbl-rx"
                        pps={counterData?.outputRx.pps ?? 0}
                        bps={counterData?.outputRx.bps ?? 0}
                        deviceId={deviceId}
                        series="output-rx"
                        color="var(--blue)"
                        history={history?.outputRx ?? []}
                        icon={<IconArrowDown />}
                    />
                    <MetricBlock
                        label="TX"
                        labelClass="dv-lbl-tx"
                        pps={counterData?.outputTx.pps ?? 0}
                        bps={counterData?.outputTx.bps ?? 0}
                        deviceId={deviceId}
                        series="output-tx"
                        color="var(--blue)"
                        history={history?.outputTx ?? []}
                        icon={<IconArrowUp />}
                    />
                    <MetricBlock
                        label="DROP"
                        labelClass="dv-lbl-drop"
                        pps={counterData?.outputDrop.pps ?? 0}
                        bps={counterData?.outputDrop.bps ?? 0}
                        deviceId={deviceId}
                        series="output-drop"
                        color="var(--red)"
                        history={history?.outputDrop ?? []}
                        icon={<IconWarning />}
                    />
                </div>
            </div>
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
                    <div className="dv-detail-title">
                        <span className="dv-detail-icon" style={{ color: iconColor }}>
                            {Icon && <Icon size={18} />}
                        </span>
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
                        <span className="dv-meta-item">Speed <span className="mono">—</span></span>
                        <span className="dv-meta-item">Duplex <span className="mono">—</span></span>
                        <span className="dv-meta-item">NUMA <span className="mono">—</span></span>
                        <span className="dv-meta-divider" />
                        <span className="dv-counter-pill">
                            <span className="dv-counter-dot dot-err" />
                            Errors <span className="dv-counter-val mono">0</span>
                        </span>
                        <span className="dv-counter-pill">
                            <span className="dv-counter-dot dot-drop" />
                            Drops <span className="dv-counter-val mono">0</span>
                        </span>
                        <span className="dv-counter-pill">
                            <span className="dv-counter-dot dot-discard" />
                            Discards <span className="dv-counter-val mono">0</span>
                        </span>
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
