import React from 'react';

const pathFor = (values: number[], w: number, h: number, pad = 2): string => {
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

const areaFor = (values: number[], w: number, h: number, pad = 2): string => {
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

export interface BigSparkProps {
    /** Unique device name used for SVG gradient ID namespacing. */
    deviceId: string;
    /** Series label used for gradient id uniqueness. */
    series: string;
    values: number[];
    color?: string;
    width?: number;
    height?: number;
}

/** Full-width sparkline used inside metric cards in the detail panel. */
export const BigSpark = ({
    deviceId,
    series,
    values,
    color = 'var(--teal)',
    width = 360,
    height = 48,
}: BigSparkProps): React.JSX.Element => {
    const gid = `dv-bg-${deviceId}-${series}`;
    const path = pathFor(values, width, height);
    const area = areaFor(values, width, height);
    return (
        <svg
            width="100%"
            height={height}
            viewBox={`0 0 ${width} ${height}`}
            preserveAspectRatio="none"
            className="dv-big-spark"
        >
            <defs>
                <linearGradient id={gid} x1="0" y1="0" x2="0" y2="1">
                    <stop offset="0%" stopColor={color} stopOpacity="0.30" />
                    <stop offset="100%" stopColor={color} stopOpacity="0" />
                </linearGradient>
            </defs>
            <path d={area} fill={`url(#${gid})`} />
            <path d={path} stroke={color} strokeWidth="1.4" fill="none" />
        </svg>
    );
};
