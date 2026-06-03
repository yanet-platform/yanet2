import React from 'react';
import { Tooltip } from '@gravity-ui/uikit';

export interface DotBadgeProps {
    label: string;
    color: string;
    tooltip?: React.ReactNode;
    tooltipClassName?: string;
}

/** Colored pill badge with a leading dot, themed via a single `color` prop. */
export const DotBadge: React.FC<DotBadgeProps> = ({ label, color, tooltip, tooltipClassName }) => {
    const pill = (
        <span
            className="yn-dot-badge"
            style={{
                '--yn-dotb-c': color,
                '--yn-dotb-bg': `color-mix(in srgb, ${color} 14%, transparent)`,
                '--yn-dotb-bd': `color-mix(in srgb, ${color} 32%, transparent)`,
            } as React.CSSProperties}
        >
            <span className="yn-dot-badge__dot" />
            {label}
        </span>
    );

    if (!tooltip) return pill;

    return (
        <Tooltip content={tooltip} openDelay={200} placement="bottom" className={tooltipClassName}>
            {pill}
        </Tooltip>
    );
};
