import React, { useCallback } from 'react';
import type { Chain } from '../types';
import { InlineEdit } from './InlineEdit';
import { WeightBadge } from './WeightBadge';
import { formatPps } from '../../../../utils';
import type { InterpolatedCounterData } from '../../../../hooks';

const CHAIN_NAME_REGEX = /^[a-z0-9_-]+$/;

interface LaneHeaderProps {
    chain: Chain;
    totalWeight: number;
    aggCounter?: InterpolatedCounterData;
    siblingChainNames: string[];
    onRename: (name: string) => void;
    onWeightChange: (weight: number) => void;
    onSelect: () => void;
}

/**
 * Left-side header of a lane: chain name, weight badge, traffic share bar, pps.
 * Uses warm-dark design tokens with monospace type.
 */
export const LaneHeader: React.FC<LaneHeaderProps> = ({
    chain,
    totalWeight,
    aggCounter,
    siblingChainNames,
    onRename,
    onWeightChange,
    onSelect,
}) => {
    const share = totalWeight > 0 ? (chain.weight / totalWeight) * 100 : 0;

    const validate = useCallback((name: string): string | null => {
        if (!name.trim()) {
            return 'Name cannot be empty';
        }
        if (name.length > 32) {
            return 'Max 32 chars';
        }
        if (!CHAIN_NAME_REGEX.test(name)) {
            return 'Only a-z, 0-9, _ and - allowed';
        }
        const others = siblingChainNames.filter(n => n !== chain.name);
        if (others.includes(name)) {
            return 'Chain name must be unique';
        }
        return null;
    }, [chain.name, siblingChainNames]);

    return (
        <div className="fn-lane-header" onClick={onSelect}>
            <div className="fn-lane-header__name-row">
                <div className="fn-lane-header__accent-bar" />
                <div
                    className="fn-lane-header__name"
                    onClick={e => e.stopPropagation()}
                >
                    <InlineEdit
                        value={chain.name}
                        onChange={onRename}
                        validate={validate}
                        hintVariant="chain"
                    />
                </div>
            </div>
            <div className="fn-lane-header__weight-row">
                <WeightBadge weight={chain.weight} onChange={onWeightChange} />
                <span className="fn-lane-header__share">{share.toFixed(0)}% of traffic</span>
            </div>
            <div className="fn-lane-header__share-bar" title={`${share.toFixed(1)}%`}>
                <div
                    className="fn-lane-header__share-fill"
                    style={{ width: `${share}%` }}
                />
            </div>
            <div className="fn-lane-header__pps">
                {aggCounter ? `${formatPps(aggCounter.pps)}` : '— pps'}
            </div>
        </div>
    );
};
