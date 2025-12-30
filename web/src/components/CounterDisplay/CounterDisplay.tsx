import React from 'react';
import { formatPps, formatBps } from '../../utils';
import './CounterDisplay.css';

export interface CounterDisplayProps {
    pps: number;
    bps: number;
    className?: string;
}

/**
 * Displays PPS and BPS counter values with icons.
 */
export const CounterDisplay: React.FC<CounterDisplayProps> = ({
    pps,
    bps,
    className = '',
}) => {
    return (
        <div className={`counter-display ${className}`.trim()}>
            <div className="counter-display__item">
                <span className="counter-display__icon">▲</span>
                <span className="counter-display__value">
                    {formatPps(pps)}
                </span>
            </div>
            <div className="counter-display__item">
                <span className="counter-display__icon">↔</span>
                <span className="counter-display__value">
                    {formatBps(bps)}
                </span>
            </div>
        </div>
    );
};
