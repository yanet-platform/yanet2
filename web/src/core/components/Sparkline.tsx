import React from 'react';
import { MetricSparkline } from '@yanet/core/components';

interface SparklineProps {
    /** Data points to render. Null means no data is available. */
    values: number[] | null;
    width?: number;
    height?: number;
    color?: string;
    fill?: boolean;
    /** Tooltip on the empty-state placeholder. */
    emptyTitle?: string;
    /** When true, the empty placeholder is sized to width×height and centered (acl). */
    sizeEmptyToBox?: boolean;
}

const Sparkline: React.FC<SparklineProps> = ({
    values,
    width = 64,
    height = 18,
    color = 'var(--yn-accent)',
    fill = true,
    emptyTitle = 'No counter history available',
    sizeEmptyToBox = false,
}) => {
    return (
        <MetricSparkline values={values} width={width} height={height} color={color} fill={fill}>
            {({ base, hasData }) => {
                if (hasData) {
                    return base;
                }
                return (
                    <span
                        className="yn-spark-empty"
                        title={emptyTitle}
                        style={sizeEmptyToBox ? { display: 'inline-flex', alignItems: 'center', justifyContent: 'center', width, height } : undefined}
                    >
                        --
                    </span>
                );
            }}
        </MetricSparkline>
    );
};

export default Sparkline;
