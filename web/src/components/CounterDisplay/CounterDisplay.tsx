import React from 'react';
import { Icon } from '@gravity-ui/uikit';
import { Layers, ArrowUpArrowDown } from '@gravity-ui/icons';
import { formatPps, formatBps } from '../../utils';
import './CounterDisplay.css';

export interface CounterDisplayProps {
    pps: number;
    bps: number;
    className?: string;
    loading?: boolean;
}

const b = (base: string, modifier?: string) => modifier ? `${base} ${base}--${modifier}` : base;

/**
 * Displays PPS and BPS counter values with icons.
 */
export const CounterDisplay: React.FC<CounterDisplayProps> = ({
    pps,
    bps,
    className = '',
    loading = false,
}) => {
    const iconState = (value: number) => loading || value === 0 ? 'inactive' : 'active';
    const valueModifier = loading ? 'loading' : undefined;

    return (
        <div className={`counter-display ${className}`.trim()}>
            <div className="counter-display__item">
                <Icon data={Layers} size={12} className={b('counter-display__icon', iconState(pps))} />
                <span className={b('counter-display__value', valueModifier)}>
                    {loading ? '-- pps' : formatPps(pps)}
                </span>
            </div>
            <div className="counter-display__item">
                <Icon data={ArrowUpArrowDown} size={12} className={b('counter-display__icon', iconState(bps))} />
                <span className={b('counter-display__value', valueModifier)}>
                    {loading ? '-- B/s' : formatBps(bps)}
                </span>
            </div>
        </div>
    );
};
