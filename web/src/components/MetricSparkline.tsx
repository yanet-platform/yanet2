import React from 'react';

interface MetricSparklineProps {
    values: number[] | null;
    width?: number;
    height?: number;
    color?: string;
    fill?: boolean;
    className?: string;
}

interface MetricSparklineRenderProps {
    width: number;
    height: number;
    hasData: boolean;
}

interface MetricSparklineRenderDataProps extends MetricSparklineRenderProps {
    linePath: string;
    fillPath: string;
    last: [number, number] | null;
}

interface MetricSparklineRenderEmptyProps extends MetricSparklineRenderProps {
    className: string;
}

export interface MetricSparklineCustomRenderProps {
    base: React.JSX.Element;
    hasData: boolean;
    renderData: (props: MetricSparklineRenderDataProps) => React.ReactNode;
    renderEmpty: (props: MetricSparklineRenderEmptyProps) => React.ReactNode;
}

interface MetricSparklineBaseProps extends MetricSparklineProps {
    children?: (props: MetricSparklineCustomRenderProps) => React.ReactNode;
}

const buildSparklineData = (values: number[], width: number, height: number): Omit<MetricSparklineRenderDataProps, 'width' | 'height' | 'hasData'> => {
    const max = Math.max(1, ...values);
    const min = Math.min(0, ...values);
    const range = Math.max(1, max - min);
    const step = width / (values.length - 1 || 1);

    const points = values.map((v, idx) => {
        const x = idx * step;
        const y = height - ((v - min) / range) * (height - 2) - 1;
        return [x, y] as [number, number];
    });

    const linePath = points
        .map((p, idx) => `${idx === 0 ? 'M' : 'L'}${p[0].toFixed(1)},${p[1].toFixed(1)}`)
        .join(' ');
    const fillPath = `${linePath} L${width},${height} L0,${height} Z`;
    const last = points[points.length - 1] ?? null;

    return { linePath, fillPath, last };
};

export const MetricSparkline: React.FC<MetricSparklineBaseProps> = ({
    values,
    width = 64,
    height = 18,
    color = 'var(--fw-accent)',
    fill = true,
    className = 'fw-spark-svg',
    children,
}) => {
    const hasData = values !== null && values.length >= 2;
    const renderEmpty = ({ className: emptyClassName }: MetricSparklineRenderEmptyProps): React.ReactNode => (
        <span className={emptyClassName}>--</span>
    );

    const renderData = ({ linePath, fillPath, last }: MetricSparklineRenderDataProps): React.ReactNode => (
        <>
            {fill && <path d={fillPath} fill={color} opacity="0.16" />}
            <path
                d={linePath}
                fill="none"
                stroke={color}
                strokeWidth="1.25"
                strokeLinejoin="round"
                strokeLinecap="round"
            />
            {last && <circle cx={last[0]} cy={last[1]} r="1.6" fill={color} />}
        </>
    );

    const base = (
        <svg
            width={width}
            height={height}
            viewBox={`0 0 ${width} ${height}`}
            className={className}
            aria-hidden="true"
        >
            {hasData && renderData({
                width,
                height,
                hasData,
                ...buildSparklineData(values, width, height),
            })}
        </svg>
    );

    if (!children) {
        return base;
    }

    return (
        <>
            {children({
                base,
                hasData,
                renderData,
                renderEmpty,
            })}
        </>
    );
};
