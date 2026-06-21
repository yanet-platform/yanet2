import React from 'react';

export type CrosshairPos = 'tl' | 'tr' | 'bl' | 'br';

export interface CrosshairProps {
    pos: CrosshairPos;
}

const PATH: Record<CrosshairPos, string> = {
    tl: 'M0 6 L0 0 L6 0',
    tr: 'M16 6 L16 0 L10 0',
    bl: 'M0 10 L0 16 L6 16',
    br: 'M16 10 L16 16 L10 16',
};

const POSITION_STYLE: Record<CrosshairPos, React.CSSProperties> = {
    tl: { top: 6, left: 6 },
    tr: { top: 6, right: 6 },
    bl: { bottom: 6, left: 6 },
    br: { bottom: 6, right: 6 },
};

/** Corner crosshair accent mark for the HUD hero panel. */
export const Crosshair: React.FC<CrosshairProps> = ({ pos }) => (
    <svg
        className={`iv-crosshair iv-crosshair--${pos}`}
        viewBox="0 0 16 16"
        style={{
            position: 'absolute',
            width: 16,
            height: 16,
            pointerEvents: 'none',
            ...POSITION_STYLE[pos],
        }}
        aria-hidden
    >
        <path d={PATH[pos]} stroke="var(--iv-accent)" strokeWidth="1" fill="none" />
    </svg>
);
