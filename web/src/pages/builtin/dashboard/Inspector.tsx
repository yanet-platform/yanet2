import React from 'react';
import { Sparkline } from '../inspect/Sparkline';
import { IconPort, IconTag, IconFn } from '../inspect/icons';
import { fmtPps, fmtBps } from '../inspect/formatters';

export interface SelectedItem {
    kind: 'device' | 'pipeline' | 'fn';
    id: string;
}

/** Structural device data derived from instance topology only. */
export interface StructuralDevice {
    id: string;
    name: string;
    kind: 'plain' | 'vlan';
    vlan?: number;
    parent?: string;
    mtu?: number;
    speed?: string;
    pipeIn?: string;
    pipeOut?: string;
}

/** Structural pipeline data derived from instance topology only. */
export interface StructuralPipeline {
    id: string;
    name: string;
    fns: string[];
}

/** Structural function data derived from instance topology only. */
export interface StructuralFunction {
    id: string;
    mod: string;
    chains: number;
}

/** Live per-frame snapshot of rates, trends, and statuses. */
export interface LiveSnapshot {
    devicesById: Map<string, {
        rxPps: number;
        rxBps: number;
        txPps: number;
        txBps: number;
        status: 'ok' | 'idle';
        trendRx: number[];
        trendTx: number[];
    }>;
    pipelinesById: Map<string, {
        pps: number;
        trend: number[];
        status: 'ok' | 'idle';
    }>;
    functionsById: Map<string, {
        pps: number;
        trend: number[];
        status: 'ok' | 'idle';
    }>;
}

export interface InspectorProps {
    selected: SelectedItem;
    onClose: () => void;
    structuralDevices: StructuralDevice[];
    structuralPipelines: StructuralPipeline[];
    structuralFunctions: StructuralFunction[];
    live: LiveSnapshot;
}

/** A small coloured status dot. */
const Dot: React.FC<{ status: 'ok' | 'idle'; size?: number }> = ({ status, size = 6 }) => (
    <span
        className="dash-dot"
        style={{
            width: size,
            height: size,
            background: status === 'ok' ? 'var(--iv-ok)' : 'var(--iv-idle)',
        }}
    />
);

/** A labelled stat row with optional hint text. */
const StatRow: React.FC<{ label: string; value: React.ReactNode; hint?: string }> = ({
    label,
    value,
    hint,
}) => (
    <div className="dash-stat-row">
        <span className="dash-stat-row__label">{label}</span>
        <span className="dash-stat-row__value">
            {value}
            {hint && <span className="dash-stat-row__hint">{hint}</span>}
        </span>
    </div>
);

/** A section sub-header with an optional item count. */
const SectionHeader: React.FC<{ children: React.ReactNode; count?: number }> = ({
    children,
    count,
}) => (
    <div className="dash-section-header">
        {children}
        {count !== undefined && <span className="dash-section-header__count">{count}</span>}
    </div>
);

/** Inspector content for a device. */
const InspectDevice: React.FC<{
    d: StructuralDevice;
    liveD: LiveSnapshot['devicesById'] extends Map<string, infer V> ? V : never;
    pipelines: StructuralPipeline[];
    livePipes: LiveSnapshot['pipelinesById'];
}> = ({ d, liveD, pipelines, livePipes }) => {
    const pIn = pipelines.find((p) => p.id === d.pipeIn);
    const pOut = pipelines.find((p) => p.id === d.pipeOut);
    const isVlan = d.kind === 'vlan';
    const status = liveD.status;
    const pInLive = pIn ? livePipes.get(pIn.id) : undefined;
    const pOutLive = pOut ? livePipes.get(pOut.id) : undefined;
    return (
        <div>
            <div className="dash-insp-name">
                <span style={{ color: isVlan ? 'var(--iv-link)' : 'var(--iv-accent)' }}>
                    {isVlan ? <IconTag size={14} /> : <IconPort size={14} />}
                </span>
                <span className="dash-insp-name__text">{d.name}</span>
                <Dot status={status} size={6} />
            </div>
            <div className="dash-insp-sub">
                {isVlan
                    ? `vlan ${d.vlan ?? '?'} on ${d.parent ?? '?'}`
                    : `physical · mtu ${d.mtu ?? '?'} · ${d.speed ?? '?'}`}
            </div>
            <SectionHeader>RX</SectionHeader>
            <StatRow label="pps" value={fmtPps(liveD.rxPps)} />
            <StatRow label="bps" value={fmtBps(liveD.rxBps)} />
            <div className="dash-sparkline-wrap">
                <Sparkline
                    data={liveD.trendRx}
                    w={266}
                    h={32}
                    color={isVlan ? 'var(--iv-link)' : 'var(--iv-accent)'}
                    fill
                />
            </div>
            <SectionHeader>TX</SectionHeader>
            <StatRow label="pps" value={fmtPps(liveD.txPps)} />
            <StatRow label="bps" value={fmtBps(liveD.txBps)} />
            <div className="dash-sparkline-wrap">
                <Sparkline
                    data={liveD.trendTx}
                    w={266}
                    h={32}
                    color={isVlan ? 'var(--iv-link)' : 'var(--iv-accent)'}
                    fill
                />
            </div>
            <SectionHeader>PIPELINES</SectionHeader>
            <StatRow label="in" value={pIn ? pIn.name : '—'} hint={pInLive ? `${fmtPps(pInLive.pps)} pps` : undefined} />
            <StatRow label="out" value={pOut ? pOut.name : '—'} hint={pOutLive ? `${fmtPps(pOutLive.pps)} pps` : undefined} />
            <SectionHeader>STATE</SectionHeader>
            <StatRow label="status" value={status} />
            <StatRow label="kind" value={d.kind} />
        </div>
    );
};

/** Inspector content for a pipeline. */
const InspectPipeline: React.FC<{
    p: StructuralPipeline;
    liveP: LiveSnapshot['pipelinesById'] extends Map<string, infer V> ? V : never;
    devices: StructuralDevice[];
    liveDevices: LiveSnapshot['devicesById'];
    functions: StructuralFunction[];
    liveFns: LiveSnapshot['functionsById'];
}> = ({ p, liveP, devices, liveDevices, functions, liveFns }) => {
    const feedDevs = devices.filter((d) => d.pipeIn === p.id);
    return (
        <div>
            <div className="dash-insp-name">
                <Dot status={liveP.status} size={6} />
                <span className="dash-insp-name__text">{p.name}</span>
            </div>
            <div className="dash-insp-sub">{p.fns.length} functions in chain</div>
            <StatRow label="pps" value={fmtPps(liveP.pps)} />
            <StatRow label="status" value={liveP.status} />
            <div className="dash-sparkline-wrap">
                <Sparkline data={liveP.trend} w={266} h={32} color="var(--iv-accent)" fill />
            </div>
            <SectionHeader count={p.fns.length}>FUNCTION CHAIN</SectionHeader>
            <div>
                {p.fns.map((fname, idx) => {
                    const fnStruct = functions.find((f) => f.id === fname);
                    const liveFn = liveFns.get(fname);
                    return (
                        <div key={fname} className="dash-chain-item">
                            <span className="dash-chain-item__idx">{idx + 1}.</span>
                            <span style={{ color: 'var(--iv-accent)' }}>
                                <IconFn size={10} />
                            </span>
                            <span className="dash-chain-item__name">
                                {(fnStruct?.id ?? fname).replace(/^fn:/, '')}
                            </span>
                            <span className="dash-chain-item__pps">{fmtPps(liveFn?.pps ?? 0)}</span>
                        </div>
                    );
                })}
            </div>
            <SectionHeader count={feedDevs.length}>FEEDING DEVICES</SectionHeader>
            <div>
                {feedDevs.slice(0, 8).map((d) => {
                    const liveDev = liveDevices.get(d.id);
                    return (
                        <div key={d.id} className="dash-feed-item">
                            <Dot status={liveDev?.status ?? 'idle'} size={4} />
                            <span style={{ color: d.kind === 'vlan' ? 'var(--iv-link)' : 'var(--iv-accent)' }}>
                                {d.kind === 'vlan' ? <IconTag size={10} /> : <IconPort size={10} />}
                            </span>
                            <span className="dash-feed-item__name">{d.name}</span>
                            <span className="dash-feed-item__pps">{fmtPps(liveDev?.rxPps ?? 0)}</span>
                        </div>
                    );
                })}
                {feedDevs.length > 8 && (
                    <span style={{ color: 'var(--iv-mute)', fontSize: 10 }}>
                        +{feedDevs.length - 8} more
                    </span>
                )}
                {feedDevs.length === 0 && (
                    <span style={{ color: 'var(--iv-mute)', fontSize: 10 }}>none</span>
                )}
            </div>
        </div>
    );
};

/** Inspector content for a function. */
const InspectFn: React.FC<{
    f: StructuralFunction;
    liveF: LiveSnapshot['functionsById'] extends Map<string, infer V> ? V : never;
    pipelines: StructuralPipeline[];
    livePipes: LiveSnapshot['pipelinesById'];
}> = ({ f, liveF, pipelines, livePipes }) => {
    const usedBy = pipelines.filter((p) => p.fns.includes(f.id));
    return (
        <div>
            <div className="dash-insp-name">
                <span style={{ color: liveF.pps > 0 ? 'var(--iv-accent)' : 'var(--iv-mute)' }}>
                    <IconFn size={14} />
                </span>
                <span className="dash-insp-name__text">{f.id}</span>
                <Dot status={liveF.status} size={6} />
            </div>
            <div className="dash-insp-sub">
                module: <span style={{ color: 'var(--iv-text-dim)' }}>{f.mod || '—'}</span>
            </div>
            <StatRow label="pps" value={fmtPps(liveF.pps)} />
            <StatRow label="status" value={liveF.status} />
            {f.mod && <StatRow label="module" value={f.mod} />}
            <div className="dash-sparkline-wrap">
                <Sparkline data={liveF.trend} w={266} h={32} color="var(--iv-accent)" fill />
            </div>
            <SectionHeader count={usedBy.length}>USED BY PIPELINES</SectionHeader>
            <div>
                {usedBy.map((p) => {
                    const liveP = livePipes.get(p.id);
                    return (
                        <div key={p.id} className="dash-feed-item">
                            <Dot status={liveP?.status ?? 'idle'} size={4} />
                            <span className="dash-feed-item__name">{p.name}</span>
                            <span className="dash-feed-item__pps">{fmtPps(liveP?.pps ?? 0)}</span>
                        </div>
                    );
                })}
            </div>
        </div>
    );
};

/** Right-side overlay inspector panel shown when a scene object is selected. */
export const Inspector: React.FC<InspectorProps> = ({
    selected,
    onClose,
    structuralDevices,
    structuralPipelines,
    structuralFunctions,
    live,
}) => {
    let content: React.ReactNode = null;
    let title = '';

    if (selected.kind === 'device') {
        const d = structuralDevices.find((x) => x.id === selected.id);
        if (!d) return null;
        const liveD = live.devicesById.get(d.id) ?? {
            rxPps: 0, rxBps: 0, txPps: 0, txBps: 0,
            status: 'idle' as const,
            trendRx: [], trendTx: [],
        };
        content = (
            <InspectDevice
                d={d}
                liveD={liveD}
                pipelines={structuralPipelines}
                livePipes={live.pipelinesById}
            />
        );
        title = 'DEVICE';
    } else if (selected.kind === 'pipeline') {
        const p = structuralPipelines.find((x) => x.id === selected.id);
        if (!p) return null;
        const liveP = live.pipelinesById.get(p.id) ?? {
            pps: 0, trend: [], status: 'idle' as const,
        };
        content = (
            <InspectPipeline
                p={p}
                liveP={liveP}
                devices={structuralDevices}
                liveDevices={live.devicesById}
                functions={structuralFunctions}
                liveFns={live.functionsById}
            />
        );
        title = 'PIPELINE';
    } else if (selected.kind === 'fn') {
        const f = structuralFunctions.find((x) => x.id === selected.id);
        if (!f) return null;
        const liveF = live.functionsById.get(f.id) ?? {
            pps: 0, trend: [], status: 'idle' as const,
        };
        content = (
            <InspectFn
                f={f}
                liveF={liveF}
                pipelines={structuralPipelines}
                livePipes={live.pipelinesById}
            />
        );
        title = 'FUNCTION';
    }

    if (!content) return null;

    return (
        <div
            className="dash-inspector"
            onClick={(e) => e.stopPropagation()}
            onMouseDown={(e) => e.stopPropagation()}
            onPointerDown={(e) => e.stopPropagation()}
        >
            <div className="dash-inspector__head">
                <span>{title}</span>
                <button className="dash-inspector__close" onClick={onClose}>
                    ×
                </button>
            </div>
            <div className="dash-inspector__body dash-scroll">{content}</div>
        </div>
    );
};
