import React, { useId, useMemo, useRef, useState } from 'react';
import { fmtPps } from './formatters';

export interface HeroSparklineProps {
    data: number[];
    w?: number;
    h?: number;
    color?: string;
}

interface HoverState {
    idx: number;
    x: number;
    y: number;
}

/**
 * Hero-sized interactive sparkline. Renders a dimmed area curve over an
 * extended viewBox and overlays a crosshair + tooltip on mouseover that
 * shows the data-point value formatted as pps.
 */
export const HeroSparkline: React.FC<HeroSparklineProps> = ({
    data,
    w = 720,
    h = 34,
    color = 'var(--iv-accent)',
}) => {
    const wrapperRef = useRef<HTMLDivElement>(null);
    const [hover, setHover] = useState<HoverState | null>(null);
    const gradientUid = useId();
    const gradientId = `iv-hero-spark-fill-${gradientUid.replace(/[^a-zA-Z0-9_-]/g, '')}`;

    const { isFlat, pts } = useMemo(() => {
        if (!data || data.length < 2) {
            return { isFlat: true, pts: [] };
        }
        const first = data[0];
        const flat = data.every((v) => v === first);
        if (flat) {
            return { isFlat: true, pts: [] };
        }
        const maxV = Math.max(...data, 1);
        const minV = Math.min(...data, 0);
        const spanV = Math.max(maxV - minV, 1);
        const step = w / (data.length - 1);
        const points = data.map((v, i) => {
            const x = i * step;
            const y = h - 2 - ((v - minV) / spanV) * (h - 4);
            return [x, y] as const;
        });
        return { isFlat: false, pts: points };
    }, [data, w, h]);

    const pathD = useMemo(() => {
        if (isFlat) return '';
        return pts
            .map(([x, y], i) => (i === 0 ? `M${x},${y}` : `L${x},${y}`))
            .join(' ');
    }, [isFlat, pts]);

    const areaD = useMemo(() => (isFlat ? '' : `${pathD} L${w},${h} L0,${h} Z`), [isFlat, pathD, w, h]);

    const handleMove = (e: React.MouseEvent<HTMLDivElement>): void => {
        if (isFlat || pts.length === 0) {
            return;
        }
        const rect = wrapperRef.current?.getBoundingClientRect();
        if (!rect || rect.width === 0) {
            return;
        }
        const ratio = (e.clientX - rect.left) / rect.width;
        const clamped = Math.max(0, Math.min(1, ratio));
        const idx = Math.round(clamped * (pts.length - 1));
        const [px, py] = pts[idx];
        setHover({ idx, x: px, y: py });
    };

    const handleLeave = (): void => {
        setHover(null);
    };

    const hoverValue = hover !== null ? data[hover.idx] : 0;
    const tooltipLeftPct = hover !== null ? (hover.x / w) * 100 : 0;

    if (isFlat) {
        const cy = h / 2;
        return (
            <div ref={wrapperRef} className="iv-hero-spark">
                <svg width={w} height={h} viewBox={`0 0 ${w} ${h}`} aria-hidden>
                    <line
                        x1={0}
                        y1={cy}
                        x2={w}
                        y2={cy}
                        stroke="var(--iv-border-strong)"
                        strokeWidth="1"
                        strokeDasharray="3 4"
                        strokeOpacity="0.6"
                    />
                </svg>
            </div>
        );
    }

    return (
        <div
            ref={wrapperRef}
            className="iv-hero-spark"
            onMouseMove={handleMove}
            onMouseLeave={handleLeave}
        >
            <svg
                width={w}
                height={h}
                viewBox={`0 0 ${w} ${h}`}
                preserveAspectRatio="none"
                aria-hidden
                className="iv-hero-spark__svg"
            >
                <defs>
                    <linearGradient id={gradientId} x1="0" y1="0" x2="0" y2="1">
                        <stop offset="0%" stopColor={color} stopOpacity="0.35" />
                        <stop offset="100%" stopColor={color} stopOpacity="0.04" />
                    </linearGradient>
                </defs>
                <g className="iv-hero-spark__curve">
                    <path d={areaD} fill={`url(#${gradientId})`} />
                    <path
                        d={pathD}
                        fill="none"
                        stroke={color}
                        strokeWidth="1.25"
                        strokeLinejoin="round"
                        strokeLinecap="round"
                    />
                </g>
                {hover !== null && (
                    <g className="iv-hero-spark__hover">
                        <line
                            x1={hover.x}
                            y1={0}
                            x2={hover.x}
                            y2={h}
                            stroke="var(--iv-text-dim)"
                            strokeWidth="1"
                            strokeOpacity="0.5"
                        />
                        <circle cx={hover.x} cy={hover.y} r="2.5" fill={color} />
                    </g>
                )}
            </svg>
            {hover !== null && (
                <div
                    className="iv-hero-spark__tooltip"
                    style={{ left: `${tooltipLeftPct}%` }}
                >
                    {fmtPps(hoverValue)} pps
                </div>
            )}
        </div>
    );
};
