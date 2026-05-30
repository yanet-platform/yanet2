import React from 'react';

const pathFor = (values: number[], w: number, h: number, max: number, pad = 1): string => {
    if (!values.length) return '';
    const stepX = (w - pad * 2) / (values.length - 1 || 1);
    let d = '';
    for (let idx = 0; idx < values.length; idx++) {
        const x = pad + idx * stepX;
        const y = h - pad - (values[idx] / max) * (h - pad * 2);
        d += (idx === 0 ? 'M' : 'L') + x.toFixed(2) + ' ' + y.toFixed(2) + ' ';
    }
    return d;
};

const areaFor = (values: number[], w: number, h: number, max: number, pad = 1): string => {
    if (!values.length) return '';
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
    const max = Math.max(1, ...rx, ...tx);
    const rxPath = pathFor(rx, width, height, max);
    const txPath = pathFor(tx, width, height, max);
    const rxArea = areaFor(rx, width, height, max);
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
