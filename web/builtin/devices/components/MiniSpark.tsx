import React from 'react';
import { pathFor, areaFor } from './sparkPath';

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
    const rxPath = pathFor(rx, width, height, max, 1);
    const txPath = pathFor(tx, width, height, max, 1);
    const rxArea = areaFor(rx, width, height, max, 1);
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
