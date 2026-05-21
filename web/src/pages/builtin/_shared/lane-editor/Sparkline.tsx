import React, { useId } from 'react';

interface SparklineProps {
    data: number[];
    width?: number;
    height?: number;
    color?: string;
}

/** Returns true when the data has no meaningful signal (all zeros or fewer than 2 distinct values). */
const isFlat = (data: number[]): boolean => {
    if (data.length < 2) {
        return true;
    }
    const first = data[0];
    return data.every(v => v === first);
};

/**
 * Minimal SVG sparkline over an array of values (no axes).
 * When data is empty or all values are identical, renders a dashed baseline placeholder
 * at the same dimensions — so the layout never shifts when real data arrives.
 */
export const Sparkline: React.FC<SparklineProps> = ({
    data,
    width = 120,
    height = 28,
    color = '#FFC061',
}) => {
    const gradientId = useId();

    if (isFlat(data)) {
        const cy = height / 2;
        return (
            <svg
                width={width}
                height={height}
                viewBox={`0 0 ${width} ${height}`}
                preserveAspectRatio="none"
                aria-hidden="true"
            >
                <line
                    x1={0}
                    y1={cy}
                    x2={width}
                    y2={cy}
                    stroke="var(--g-color-line-generic)"
                    strokeWidth="1"
                    strokeDasharray="3 4"
                    strokeOpacity="0.6"
                />
            </svg>
        );
    }

    const max = Math.max(...data, 1);
    const step = width / (data.length - 1);

    const points = data.map((v, idx) => {
        const x = idx * step;
        const y = height - (v / max) * (height - 2) - 1;
        return `${x.toFixed(1)},${y.toFixed(1)}`;
    });

    const d = `M${points.join('L')}`;
    const fillPath = `${d}L${width},${height}L0,${height}Z`;

    return (
        <svg
            width={width}
            height={height}
            viewBox={`0 0 ${width} ${height}`}
            preserveAspectRatio="none"
            aria-hidden="true"
        >
            <defs>
                <linearGradient id={gradientId} x1="0" y1="0" x2="0" y2="1">
                    <stop offset="0%" stopColor={color} stopOpacity="0.35" />
                    <stop offset="100%" stopColor={color} stopOpacity="0.04" />
                </linearGradient>
            </defs>
            <path
                d={fillPath}
                fill={`url(#${gradientId})`}
            />
            <path
                d={d}
                fill="none"
                stroke={color}
                strokeWidth="1.5"
                strokeLinejoin="round"
                strokeLinecap="round"
            />
        </svg>
    );
};
