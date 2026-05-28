import React, { useState, useCallback, useEffect, useMemo } from 'react';
import {
    IconPlain,
    IconVlan,
    IconHdd,
    IconArrowDown,
    IconArrowUp,
    IconSave,
} from './components/Icons';
import { DeviceDiffModal } from './components/DeviceDiffModal';
import { BigSpark } from './components/BigSpark';
import { fmtPps, fmtBps } from './components/MiniSpark';
import { PipelineTable } from './PipelineTable';
import type { LocalDevice } from './types';
import type { PipelineId } from '../../../api/pipelines';
import type { DevicePipeline } from '../../../api/devices';
import type { DeviceCounterData } from '../../../hooks/useDeviceCounters';
import type { CounterHistoryEntry } from '../../../hooks/useCounterHistory';

export interface DeviceDetailsProps {
    device: LocalDevice | null;
    loadPipelineList: () => Promise<PipelineId[]>;
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
        <div className="dv-metric-val mono">
            {isPps ? fmtPps(value) : fmtBps(value)}
            {isPps && <span className="dv-metric-unit">pps</span>}
        </div>
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

export const DeviceDetails: React.FC<DeviceDetailsProps> = ({
    device,
    loadPipelineList,
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

    const handleSaveClick = useCallback(() => {
        setDiffOpen(true);
    }, []);

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

    const handleInputPipelinesChange = useCallback((pipelines: DevicePipeline[]) => {
        onUpdate({ inputPipelines: pipelines });
    }, [onUpdate]);

    const handleOutputPipelinesChange = useCallback((pipelines: DevicePipeline[]) => {
        onUpdate({ outputPipelines: pipelines });
    }, [onUpdate]);

    const rxPpsHistory = useMemo(() => history?.rx ?? [], [history]);
    const txPpsHistory = useMemo(() => history?.tx ?? [], [history]);
    const rxBpsHistory = useMemo(() => history?.rxBytes ?? [], [history]);
    const txBpsHistory = useMemo(() => history?.txBytes ?? [], [history]);

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

    const isVlan = device.type === 'vlan';
    const name = device.id.name || '';
    const iconColor = isVlan ? 'var(--violet)' : 'var(--teal)';
    const rxPps = counterData?.rx.pps ?? 0;
    const rxBps = counterData?.rx.bps ?? 0;
    const txPps = counterData?.tx.pps ?? 0;
    const txBps = counterData?.tx.bps ?? 0;
    const canSave = (device.isDirty || device.isNew) && !saving;
    const serverDevice = name ? getServerDevice(name) : null;

    return (
        <div className="dv-detail">
            <div className="dv-detail-hd">
                <div className="dv-detail-hd-left">
                    <span className="dv-detail-icon" style={{ color: iconColor }}>
                        {isVlan ? <IconVlan size={20} /> : <IconPlain size={20} />}
                    </span>
                    <div className="dv-detail-title-wrap">
                        <div className="dv-detail-title">
                            <span className="dv-detail-name">{name}</span>
                            <span className={"dv-kind-tag " + (isVlan ? 'kind-vlan' : 'kind-plain')}>
                                {isVlan ? "VLAN · " + (device.vlanId ?? '—') : 'PHYSICAL'}
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
                            {isVlan ? (
                                <span className="muted">no parent</span>
                            ) : (
                                <span>— · —</span>
                            )}
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
                <div className="dv-metric-grid">
                    <MetricBlock
                        subLabel="packets / sec"
                        isRx={true}
                        isPps={true}
                        value={rxPps}
                        deviceId={name}
                        series="rx-pps"
                        color="var(--teal)"
                        history={rxPpsHistory}
                    />
                    <MetricBlock
                        subLabel="bytes / sec"
                        isRx={true}
                        isPps={false}
                        value={rxBps}
                        deviceId={name}
                        series="rx-bps"
                        color="var(--teal)"
                        history={rxBpsHistory}
                    />
                    <MetricBlock
                        subLabel="packets / sec"
                        isRx={false}
                        isPps={true}
                        value={txPps}
                        deviceId={name}
                        series="tx-pps"
                        color="var(--blue)"
                        history={txPpsHistory}
                    />
                    <MetricBlock
                        subLabel="bytes / sec"
                        isRx={false}
                        isPps={false}
                        value={txBps}
                        deviceId={name}
                        series="tx-bps"
                        color="var(--blue)"
                        history={txBpsHistory}
                    />
                </div>

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
                        {isVlan ? (
                            <>
                                <PropCell label="VLAN ID" value={device.vlanId !== undefined ? String(device.vlanId) : '—'} mono />
                                <PropCell label="Parent device" value="—" mono />
                            </>
                        ) : (
                            <>
                                <PropCell label="Driver" value="—" mono />
                                <PropCell label="PCI bus" value="—" mono />
                            </>
                        )}
                        <PropCell label="NUMA node" value="—" mono />
                        <PropCell label="Type" value={isVlan ? 'logical (vlan)' : 'physical (plain)'} />
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
