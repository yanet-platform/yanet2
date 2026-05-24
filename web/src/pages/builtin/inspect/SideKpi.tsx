import React from 'react';

export interface SideKpiProps {
    label: string;
    primary: React.ReactNode;
    secondary?: React.ReactNode;
}

/** Compact KPI cell used in the left and right columns of the HUD hero. */
export const SideKpi: React.FC<SideKpiProps> = ({ label, primary, secondary }) => (
    <div className="iv-side-kpi">
        <div className="iv-side-kpi__label">{label}</div>
        <div className="iv-side-kpi__primary">{primary}</div>
        {secondary !== undefined && secondary !== '' && (
            <div className="iv-side-kpi__secondary">{secondary}</div>
        )}
    </div>
);
