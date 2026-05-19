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
 * Pure SVG sparkline component.
 * When values is null or empty, renders an empty placeholder slot.
 */
const Sparkline: React.FC<SparklineProps> = ({
    values,
    width = 64,
    height = 18,
    color = 'var(--fwng-accent)',
    fill = true,
}) => {
    if (!values || values.length < 2) {
        return (
            <span
                className="fwng-spark-empty"
                title="No counter history available from backend"
            >
                --
            </span>
        );
    }

    const max = Math.max(1, ...values);
    const min = Math.min(0, ...values);
    const range = Math.max(1, max - min);
    const step = width / (values.length - 1 || 1);

    const points = values.map((v, idx) => {
        const x = idx * step;
        const y = height - ((v - min) / range) * (height - 2) - 1;
        return [x, y] as [number, number];
    });

    const path = points
        .map((p, idx) => `${idx === 0 ? 'M' : 'L'}${p[0].toFixed(1)},${p[1].toFixed(1)}`)
        .join(' ');
    const fillPath = `${path} L${width},${height} L0,${height} Z`;
    const last = points[points.length - 1];

    return (
        <svg
            width={width}
            height={height}
            viewBox={`0 0 ${width} ${height}`}
            className="fwng-spark-svg"
            aria-hidden="true"
        >
            {fill && <path d={fillPath} fill={color} opacity="0.16" />}
            <path
                d={path}
                fill="none"
                stroke={color}
                strokeWidth="1.25"
                strokeLinejoin="round"
                strokeLinecap="round"
            />
            {last && <circle cx={last[0]} cy={last[1]} r="1.6" fill={color} />}
        </svg>
    );
};

export default Sparkline;
