import React from 'react';

export interface InspectPageFooterProps {
    lastUpdate: Date | null;
}

const formatTime = (d: Date): string => {
    const hh = String(d.getHours()).padStart(2, '0');
    const mm = String(d.getMinutes()).padStart(2, '0');
    const ss = String(d.getSeconds()).padStart(2, '0');
    return `${hh}:${mm}:${ss}`;
};

export const InspectPageFooter: React.FC<InspectPageFooterProps> = ({ lastUpdate }) => {
    const ts = lastUpdate ? formatTime(lastUpdate) : '—';
    return (
        <div className="inspect-page-footer inspect-mono">
            last update {ts} · polling 1.5s · controlplane reachable
        </div>
    );
};
