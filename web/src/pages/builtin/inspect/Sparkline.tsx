import { useId } from 'react';
import React from 'react';

export interface SparklineProps {
    data: number[];
    color?: string;
    w?: number;
    h?: number;
    fill?: boolean;
    strokeWidth?: number;
}

const isFlat = (data: number[]): boolean => {
    if (data.length < 2) return true;
    const first = data[0];
    return data.every(v => v === first);
};

export const Sparkline: React.FC<SparklineProps> = ({
    data,
    color = 'currentColor',
    w = 80,
    h = 22,
    fill = true,
    strokeWidth = 1.25,
}) => {
    const gradientUid = useId();
    const gradientId = `spark-fill-${gradientUid.replace(/[^a-zA-Z0-9_-]/g, '')}`;

    if (isFlat(data ?? [])) {
        const cy = h / 2;
        return (
            <svg width={w} height={h} viewBox={`0 0 ${w} ${h}`} aria-hidden style={{ overflow: 'visible', display: 'block' }}>
                <line
                    x1={0} y1={cy} x2={w} y2={cy}
                    stroke="var(--g-color-line-generic)"
                    strokeWidth="1"
                    strokeDasharray="3 4"
                    strokeOpacity="0.6"
                />
            </svg>
        );
    }

    const max = Math.max(...data, 1);
    const min = Math.min(...data, 0);
    const span = Math.max(max - min, 1);
    const step = w / (data.length - 1 || 1);

    const pts = data.map((v, i) => {
        const x = i * step;
        const y = h - 2 - ((v - min) / span) * (h - 4);
        return [x, y] as const;
    });

    const d = pts
        .map(([x, y], i) => (i === 0 ? `M${x},${y}` : `L${x},${y}`))
        .join(' ');
    const area = fill ? `${d} L${w},${h} L0,${h} Z` : null;

    return (
        <svg
            width={w}
            height={h}
            viewBox={`0 0 ${w} ${h}`}
            aria-hidden
            style={{ overflow: 'visible', display: 'block' }}
        >
            {fill && area && (
                <defs>
                    <linearGradient id={gradientId} x1="0" y1="0" x2="0" y2="1">
                        <stop offset="0%" stopColor={color} stopOpacity="0.35" />
                        <stop offset="100%" stopColor={color} stopOpacity="0.04" />
                    </linearGradient>
                </defs>
            )}
            {fill && area && <path d={area} fill={`url(#${gradientId})`} />}
            <path
                d={d}
                fill="none"
                stroke={color}
                strokeWidth={strokeWidth}
                strokeLinejoin="round"
                strokeLinecap="round"
            />
        </svg>
    );
};
