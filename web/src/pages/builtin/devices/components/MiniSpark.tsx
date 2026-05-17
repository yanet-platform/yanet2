import React from 'react';

/** Format packets per second without the unit suffix (used inside sparkline labels). */
export const fmtPps = (n: number): string => {
    if (n === 0) return '0';
    if (n >= 1e9) return (n / 1e9).toFixed(2) + 'G';
    if (n >= 1e6) return (n / 1e6).toFixed(1) + 'M';
    if (n >= 1e3) return (n / 1e3).toFixed(1) + 'K';
    return String(Math.round(n));
};

/** Format bytes per second with a unit suffix. */
export const fmtBps = (n: number): string => {
    if (n === 0) return '0';
    if (n >= 1e9) return (n / 1e9).toFixed(2) + ' GB/s';
    if (n >= 1e6) return (n / 1e6).toFixed(1) + ' MB/s';
    if (n >= 1e3) return (n / 1e3).toFixed(1) + ' KB/s';
    return n + ' B/s';
};

const pathFor = (values: number[], w: number, h: number, pad = 1): string => {
    if (!values.length) return '';
    const max = Math.max(1, ...values);
    const stepX = (w - pad * 2) / (values.length - 1 || 1);
    let d = '';
    for (let idx = 0; idx < values.length; idx++) {
        const x = pad + idx * stepX;
        const y = h - pad - (values[idx] / max) * (h - pad * 2);
        d += (idx === 0 ? 'M' : 'L') + x.toFixed(2) + ' ' + y.toFixed(2) + ' ';
    }
    return d;
};

const areaFor = (values: number[], w: number, h: number, pad = 1): string => {
    if (!values.length) return '';
    const max = Math.max(1, ...values);
    const stepX = (w - pad * 2) / (values.length - 1 || 1);
    let d = `M${pad} ${h - pad} `;
    for (let idx = 0; idx < values.length; idx++) {
        const x = pad + idx * stepX;
        const y = h - pad - (values[idx] / max) * (h - pad * 2);
        d += 'L' + x.toFixed(2) + ' ' + y.toFixed(2) + ' ';
    }
    d += `L${w - pad} ${h - pad} Z`;
    return d;
};

export interface MiniSparkProps {
    /** Unique device name used for SVG gradient ID. */
    deviceId: string;
    rx: number[];
    tx: number[];
    width?: number;
    height?: number;
}

/** Compact dual-series sparkline shown in list rows. */
export const MiniSpark = ({ deviceId, rx, tx, width = 72, height = 24 }: MiniSparkProps): React.JSX.Element => {
    const gid = `dv-g-${deviceId}`;
    const rxPath = pathFor(rx, width, height);
    const txPath = pathFor(tx, width, height);
    const rxArea = areaFor(rx, width, height);
    return (
        <svg
            width={width}
            height={height}
            viewBox={`0 0 ${width} ${height}`}
            className="dv-mini-spark"
        >
            <defs>
                <linearGradient id={gid} x1="0" y1="0" x2="0" y2="1">
                    <stop offset="0%" stopColor="var(--teal)" stopOpacity="0.35" />
                    <stop offset="100%" stopColor="var(--teal)" stopOpacity="0" />
                </linearGradient>
            </defs>
            <path d={rxArea} fill={`url(#${gid})`} />
            <path d={rxPath} stroke="var(--teal)" strokeWidth="1.2" fill="none" />
            <path d={txPath} stroke="var(--blue)" strokeWidth="1.2" fill="none" opacity="0.85" />
        </svg>
    );
};
