import React from 'react';

interface SparklineProps {
    /** Data points to render. Null means no data is available. */
    values: number[] | null;
    width?: number;
    height?: number;
    color?: string;
    fill?: boolean;
}

/**
 * Pure SVG sparkline component for pdump pps history.
 * When values is null or empty, renders an empty placeholder slot.
 */
const Sparkline: React.FC<SparklineProps> = ({
    values,
    width = 64,
    height = 18,
    color = 'var(--fw-accent)',
    fill = true,
}) => {
    const hasData = values !== null && values.length >= 2;

    let linePath: string | null = null;
    let fillPath: string | null = null;
    let last: [number, number] | null = null;

    if (hasData) {
        const max = Math.max(1, ...values!);
        const min = Math.min(0, ...values!);
        const range = Math.max(1, max - min);
        const step = width / (values!.length - 1 || 1);

        const points = values!.map((v, idx) => {
            const x = idx * step;
            const y = height - ((v - min) / range) * (height - 2) - 1;
            return [x, y] as [number, number];
        });

        linePath = points
            .map((p, idx) => `${idx === 0 ? 'M' : 'L'}${p[0].toFixed(1)},${p[1].toFixed(1)}`)
            .join(' ');
        fillPath = `${linePath} L${width},${height} L0,${height} Z`;
        last = points[points.length - 1] ?? null;
    }

    return (
        <svg
            width={width}
            height={height}
            viewBox={`0 0 ${width} ${height}`}
            className="fw-spark-svg"
            aria-hidden="true"
        >
            {hasData && fill && fillPath && (
                <path d={fillPath} fill={color} opacity="0.16" />
            )}
            {hasData && linePath && (
                <path
                    d={linePath}
                    fill="none"
                    stroke={color}
                    strokeWidth="1.25"
                    strokeLinejoin="round"
                    strokeLinecap="round"
                />
            )}
            {hasData && last && <circle cx={last[0]} cy={last[1]} r="1.6" fill={color} />}
        </svg>
    );
};

export default Sparkline;
