import React from 'react';

export interface Inspect3dPageFooterProps {
    lastUpdate: Date | null;
}

const formatTime = (d: Date): string => {
    const hh = String(d.getHours()).padStart(2, '0');
    const mm = String(d.getMinutes()).padStart(2, '0');
    const ss = String(d.getSeconds()).padStart(2, '0');
    return `${hh}:${mm}:${ss}`;
};

/** Footer showing last update time and connectivity status. */
export const Inspect3dPageFooter: React.FC<Inspect3dPageFooterProps> = ({ lastUpdate }) => {
    const ts = lastUpdate ? formatTime(lastUpdate) : '—';
    return (
        <div className="inspect-3d-page-footer">
            last update {ts} · controlplane reachable
        </div>
    );
};
