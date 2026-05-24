import React from 'react';
import { Sparkline } from '../inspect/Sparkline';
import { IconPort, IconTag, IconFn } from '../inspect/icons';
import { fmtPps, fmtBps } from '../inspect/formatters';

export interface DeviceData {
    id: string;
    name: string;
    kind: 'plain' | 'vlan';
    rxPps: number;
    rxBps: number;
    txPps: number;
    txBps: number;
    status: 'ok' | 'idle';
    pipeIn?: string;
    pipeOut?: string;
    trendRx: number[];
    trendTx: number[];
    vlan?: number;
    parent?: string;
    mtu?: number;
    speed?: string;
}

export interface PipelineData {
    id: string;
    name: string;
    fns: string[];
    pps: number;
    trend: number[];
    status: 'ok' | 'idle';
}

export interface FunctionData {
    id: string;
    mod: string;
    pps: number;
    trend: number[];
    status: 'ok' | 'idle';
}

export interface SelectedItem {
    kind: 'device' | 'pipeline' | 'fn';
    id: string;
}

export interface InspectorProps {
    selected: SelectedItem;
    onClose: () => void;
    devices: DeviceData[];
    pipelines: PipelineData[];
    functions: FunctionData[];
}

/** A small coloured status dot. */
const Dot: React.FC<{ status: 'ok' | 'idle'; size?: number }> = ({ status, size = 6 }) => (
    <span
        className="iv3-dot"
        style={{
            width: size,
            height: size,
            background: status === 'ok' ? 'var(--iv3-ok)' : 'var(--iv3-idle)',
        }}
    />
);

/** A labelled stat row with optional hint text. */
const StatRow: React.FC<{ label: string; value: React.ReactNode; hint?: string }> = ({
    label,
    value,
    hint,
}) => (
    <div className="iv3-stat-row">
        <span className="iv3-stat-row__label">{label}</span>
        <span className="iv3-stat-row__value">
            {value}
            {hint && <span className="iv3-stat-row__hint">{hint}</span>}
        </span>
    </div>
);

/** A section sub-header with an optional item count. */
const SectionHeader: React.FC<{ children: React.ReactNode; count?: number }> = ({
    children,
    count,
}) => (
    <div className="iv3-section-header">
        {children}
        {count !== undefined && <span className="iv3-section-header__count">{count}</span>}
    </div>
);

/** Inspector content for a device. */
const InspectDevice: React.FC<{
    d: DeviceData;
    pipelines: PipelineData[];
}> = ({ d, pipelines }) => {
    const pIn = pipelines.find((p) => p.id === d.pipeIn);
    const pOut = pipelines.find((p) => p.id === d.pipeOut);
    const isVlan = d.kind === 'vlan';
    return (
        <div>
            <div className="iv3-insp-name">
                <span style={{ color: isVlan ? 'var(--iv3-link)' : 'var(--iv3-accent)' }}>
                    {isVlan ? <IconTag size={14} /> : <IconPort size={14} />}
                </span>
                <span className="iv3-insp-name__text">{d.name}</span>
                <Dot status={d.status} size={6} />
            </div>
            <div className="iv3-insp-sub">
                {isVlan
                    ? `vlan ${d.vlan ?? '?'} on ${d.parent ?? '?'}`
                    : `physical · mtu ${d.mtu ?? '?'} · ${d.speed ?? '?'}`}
            </div>
            <SectionHeader>RX</SectionHeader>
            <StatRow label="pps" value={fmtPps(d.rxPps)} />
            <StatRow label="bps" value={fmtBps(d.rxBps)} />
            <div className="iv3-sparkline-wrap">
                <Sparkline
                    data={d.trendRx}
                    w={266}
                    h={32}
                    color={isVlan ? 'var(--iv3-link)' : 'var(--iv3-accent)'}
                    fill
                />
            </div>
            <SectionHeader>TX</SectionHeader>
            <StatRow label="pps" value={fmtPps(d.txPps)} />
            <StatRow label="bps" value={fmtBps(d.txBps)} />
            <div className="iv3-sparkline-wrap">
                <Sparkline
                    data={d.trendTx}
                    w={266}
                    h={32}
                    color={isVlan ? 'var(--iv3-link)' : 'var(--iv3-accent)'}
                    fill
                />
            </div>
            <SectionHeader>PIPELINES</SectionHeader>
            <StatRow label="in" value={pIn ? pIn.name : '—'} />
            <StatRow label="out" value={pOut ? pOut.name : '—'} />
            <SectionHeader>STATE</SectionHeader>
            <StatRow label="status" value={d.status} />
            <StatRow label="kind" value={d.kind} />
        </div>
    );
};

/** Inspector content for a pipeline. */
const InspectPipeline: React.FC<{
    p: PipelineData;
    devices: DeviceData[];
    functions: FunctionData[];
}> = ({ p, devices, functions }) => {
    const feedDevs = devices.filter((d) => d.pipeIn === p.id);
    const fnObjs = p.fns
        .map((fname) => functions.find((f) => f.id === fname))
        .filter((f): f is FunctionData => f !== undefined);
    return (
        <div>
            <div className="iv3-insp-name">
                <Dot status={p.status} size={6} />
                <span className="iv3-insp-name__text">{p.name}</span>
            </div>
            <div className="iv3-insp-sub">{p.fns.length} functions in chain</div>
            <StatRow label="pps" value={fmtPps(p.pps)} />
            <StatRow label="status" value={p.status} />
            <div className="iv3-sparkline-wrap">
                <Sparkline data={p.trend} w={266} h={32} color="var(--iv3-accent)" fill />
            </div>
            <SectionHeader count={p.fns.length}>FUNCTION CHAIN</SectionHeader>
            <div>
                {fnObjs.map((f, idx) => (
                    <div key={f.id} className="iv3-chain-item">
                        <span className="iv3-chain-item__idx">{idx + 1}.</span>
                        <span style={{ color: 'var(--iv3-accent)' }}>
                            <IconFn size={10} />
                        </span>
                        <span className="iv3-chain-item__name">
                            {f.id.replace(/^fn:/, '')}
                        </span>
                        <span className="iv3-chain-item__pps">{fmtPps(f.pps)}</span>
                    </div>
                ))}
            </div>
            <SectionHeader count={feedDevs.length}>FEEDING DEVICES</SectionHeader>
            <div>
                {feedDevs.slice(0, 8).map((d) => (
                    <div key={d.id} className="iv3-feed-item">
                        <Dot status={d.status} size={4} />
                        <span style={{ color: d.kind === 'vlan' ? 'var(--iv3-link)' : 'var(--iv3-accent)' }}>
                            {d.kind === 'vlan' ? <IconTag size={10} /> : <IconPort size={10} />}
                        </span>
                        <span className="iv3-feed-item__name">{d.name}</span>
                        <span className="iv3-feed-item__pps">{fmtPps(d.rxPps)}</span>
                    </div>
                ))}
                {feedDevs.length > 8 && (
                    <span style={{ color: 'var(--iv3-mute)', fontSize: 10 }}>
                        +{feedDevs.length - 8} more
                    </span>
                )}
                {feedDevs.length === 0 && (
                    <span style={{ color: 'var(--iv3-mute)', fontSize: 10 }}>none</span>
                )}
            </div>
        </div>
    );
};

/** Inspector content for a function. */
const InspectFn: React.FC<{
    f: FunctionData;
    pipelines: PipelineData[];
}> = ({ f, pipelines }) => {
    const usedBy = pipelines.filter((p) => p.fns.includes(f.id));
    return (
        <div>
            <div className="iv3-insp-name">
                <span style={{ color: f.pps > 0 ? 'var(--iv3-accent)' : 'var(--iv3-mute)' }}>
                    <IconFn size={14} />
                </span>
                <span className="iv3-insp-name__text">{f.id}</span>
                <Dot status={f.status} size={6} />
            </div>
            <div className="iv3-insp-sub">
                module: <span style={{ color: 'var(--iv3-text-dim)' }}>{f.mod || '—'}</span>
            </div>
            <StatRow label="pps" value={fmtPps(f.pps)} />
            <StatRow label="status" value={f.status} />
            {f.mod && <StatRow label="module" value={f.mod} />}
            <div className="iv3-sparkline-wrap">
                <Sparkline data={f.trend} w={266} h={32} color="var(--iv3-accent)" fill />
            </div>
            <SectionHeader count={usedBy.length}>USED BY PIPELINES</SectionHeader>
            <div>
                {usedBy.map((p) => (
                    <div key={p.id} className="iv3-feed-item">
                        <Dot status={p.status} size={4} />
                        <span className="iv3-feed-item__name">{p.name}</span>
                        <span className="iv3-feed-item__pps">{fmtPps(p.pps)}</span>
                    </div>
                ))}
            </div>
        </div>
    );
};

/** Right-side overlay inspector panel shown when a scene object is selected. */
export const Inspector: React.FC<InspectorProps> = ({
    selected,
    onClose,
    devices,
    pipelines,
    functions,
}) => {
    let content: React.ReactNode = null;
    let title = '';

    if (selected.kind === 'device') {
        const d = devices.find((x) => x.id === selected.id);
        if (!d) return null;
        content = <InspectDevice d={d} pipelines={pipelines} />;
        title = 'DEVICE';
    } else if (selected.kind === 'pipeline') {
        const p = pipelines.find((x) => x.id === selected.id);
        if (!p) return null;
        content = <InspectPipeline p={p} devices={devices} functions={functions} />;
        title = 'PIPELINE';
    } else if (selected.kind === 'fn') {
        const f = functions.find((x) => x.id === selected.id);
        if (!f) return null;
        content = <InspectFn f={f} pipelines={pipelines} />;
        title = 'FUNCTION';
    }

    if (!content) return null;

    return (
        <div
            className="iv3-inspector"
            onClick={(e) => e.stopPropagation()}
            onMouseDown={(e) => e.stopPropagation()}
            onPointerDown={(e) => e.stopPropagation()}
        >
            <div className="iv3-inspector__head">
                <span>{title}</span>
                <button className="iv3-inspector__close" onClick={onClose}>
                    ×
                </button>
            </div>
            <div className="iv3-inspector__body iv3-scroll">{content}</div>
        </div>
    );
};
