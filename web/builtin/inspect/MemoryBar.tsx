import React from 'react';

export interface MemoryBarProps {
    used: number;
    limit: number;
    height?: number;
    cells?: number;
    color?: string;
    muted?: string;
}

/** HUD-style segmented memory bar. Lit cells are proportional to used/limit. */
export const MemoryBar: React.FC<MemoryBarProps> = ({
    used,
    limit,
    height = 4,
    cells = 24,
    color = 'var(--iv-accent)',
    muted = 'var(--iv-faint)',
}) => {
    const pct = limit > 0 ? Math.min(1, used / limit) : 0;
    const lit = Math.round(pct * cells);
    const fill =
        pct > 0.9
            ? 'var(--iv-danger)'
            : pct > 0.7
              ? 'var(--iv-warn)'
              : color;

    return (
        <div
            style={{
                display: 'flex',
                gap: 1,
                height,
                width: '100%',
                alignItems: 'stretch',
            }}
        >
            {Array.from({ length: cells }, (_, idx) => (
                <div
                    key={idx}
                    style={{
                        flex: 1,
                        background: idx < lit ? fill : muted,
                        opacity: idx < lit ? 0.55 + (idx / cells) * 0.45 : 1,
                    }}
                />
            ))}
        </div>
    );
};
