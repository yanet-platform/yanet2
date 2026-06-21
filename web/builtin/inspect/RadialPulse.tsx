import React, { useEffect, useState } from 'react';

const SIZE = 200;
const CX = SIZE / 2;
const CY = SIZE / 2;
const R_OUTER = SIZE / 2 - 6;
const R_INNER = SIZE / 2 - 24;
const R_TICK_OUTER = SIZE / 2 - 6;
const R_TICK_INNER = SIZE / 2 - 14;

/** Animated radial pulse SVG centered behind the throughput number. */
export const RadialPulse: React.FC = () => {
    const [pulse, setPulse] = useState(0);

    useEffect(() => {
        const id = setInterval(() => setPulse((x) => x + 1), 1500);
        return () => clearInterval(id);
    }, []);

    const ticks = Array.from({ length: 12 }, (_, idx) => {
        const angle = (idx / 12) * Math.PI * 2 - Math.PI / 2;
        const x1 = CX + Math.cos(angle) * R_TICK_OUTER;
        const y1 = CY + Math.sin(angle) * R_TICK_OUTER;
        const x2 = CX + Math.cos(angle) * R_TICK_INNER;
        const y2 = CY + Math.sin(angle) * R_TICK_INNER;
        return { x1, y1, x2, y2 };
    });

    return (
        <svg
            width={SIZE}
            height={SIZE}
            viewBox={`0 0 ${SIZE} ${SIZE}`}
            style={{
                position: 'absolute',
                top: '50%',
                left: '50%',
                transform: 'translate(-50%, -50%)',
                opacity: 0.4,
                pointerEvents: 'none',
            }}
            aria-hidden
        >
            <circle cx={CX} cy={CY} r={R_OUTER} stroke="var(--iv-accent)" strokeWidth="0.5" fill="none" />
            <circle
                cx={CX}
                cy={CY}
                r={R_INNER}
                stroke="var(--iv-border-strong)"
                strokeWidth="0.4"
                fill="none"
                strokeDasharray="2 4"
            />
            {ticks.map((t, idx) => (
                <line
                    key={idx}
                    x1={t.x1}
                    y1={t.y1}
                    x2={t.x2}
                    y2={t.y2}
                    stroke="var(--iv-accent)"
                    strokeWidth="0.7"
                    opacity="0.6"
                />
            ))}
            <circle
                key={pulse}
                cx={CX}
                cy={CY}
                r={R_OUTER}
                stroke="var(--iv-accent)"
                strokeWidth="1"
                fill="none"
                className="iv-radial-pulse-ring"
            />
        </svg>
    );
};
