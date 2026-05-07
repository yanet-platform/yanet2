import React from 'react';
import { Sparkline } from './Sparkline';

export interface KpiCellProps {
    label: string;
    value: React.ReactNode;
    hint?: React.ReactNode;
    series?: number[];
    color?: string;
    emphasize?: boolean;
}

export const KpiCell: React.FC<KpiCellProps> = ({
    label,
    value,
    hint,
    series,
    color = 'var(--inspect-accent)',
    emphasize = false,
}) => {
    const valueClass = emphasize
        ? 'inspect-kpi-value inspect-kpi-value--lg inspect-num'
        : 'inspect-kpi-value inspect-num';

    return (
        <div className="inspect-kpi">
            <div className="inspect-kpi-label">{label}</div>
            <div className="inspect-kpi-row">
                <div className={valueClass}>{value}</div>
                {series && series.length > 0 && (
                    <Sparkline data={series} color={color} w={64} h={20} fill />
                )}
            </div>
            {hint !== undefined && <div className="inspect-kpi-hint">{hint}</div>}
        </div>
    );
};
