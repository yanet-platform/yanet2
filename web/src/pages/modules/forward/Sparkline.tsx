import React from 'react';
import { MetricSparkline } from '../../../components';

interface SparklineProps {
    /** Data points to render. Null means no data is available. */
    values: number[] | null;
    width?: number;
    height?: number;
    color?: string;
    fill?: boolean;
}

const Sparkline: React.FC<SparklineProps> = ({
    values,
    width = 64,
    height = 18,
    color = 'var(--fw-accent)',
    fill = true,
}) => {
    return (
        <MetricSparkline values={values} width={width} height={height} color={color} fill={fill}>
            {({ base, hasData }) => {
                if (hasData) {
                    return base;
                }
                return (
                    <span
                        className="fw-spark-empty"
                        title="No counter history available from backend"
                    >
                        --
                    </span>
                );
            }}
        </MetricSparkline>
    );
};

export default Sparkline;
