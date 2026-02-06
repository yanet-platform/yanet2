import React from 'react';
import { Icon } from '@gravity-ui/uikit';
import { Layers, ArrowUpArrowDown } from '@gravity-ui/icons';
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
    const ppsIconClass = pps > 0 ? 'counter-display__icon--active' : 'counter-display__icon--inactive';
    const bpsIconClass = bps > 0 ? 'counter-display__icon--active' : 'counter-display__icon--inactive';

    return (
        <div className={`counter-display ${className}`.trim()}>
            <div className="counter-display__item">
                <Icon data={Layers} size={12} className={`counter-display__icon ${ppsIconClass}`} />
                <span className="counter-display__value">
                    {formatPps(pps)}
                </span>
            </div>
            <div className="counter-display__item">
                <Icon data={ArrowUpArrowDown} size={12} className={`counter-display__icon ${bpsIconClass}`} />
                <span className="counter-display__value">
                    {formatBps(bps)}
                </span>
            </div>
        </div>
    );
};
