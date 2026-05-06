import React from 'react';
import { Sparkline } from './Sparkline';
import { fmtPkts } from './formatters';

export interface FunctionTileProps {
    name: string;
    chains: string[];
    pps: number;
    series: number[];
}

export const FunctionTile: React.FC<FunctionTileProps> = ({ name, chains, pps, series }) => {
    return (
        <div className="inspect-fn">
            <div className="inspect-fn-head">
                <span className="inspect-mono inspect-fn-name">{name}</span>
                <span className="inspect-fn-chains">{chains.length} chain</span>
            </div>
            <div className="inspect-fn-items">
                {chains.length === 0 ? (
                    <span className="inspect-fn-item-empty">—</span>
                ) : (
                    chains.map((c, idx) => (
                        <span
                            key={`${c}-${idx}`}
                            className="inspect-mono inspect-fn-item"
                        >
                            {c}
                        </span>
                    ))
                )}
            </div>
            <div className="inspect-fn-spark">
                <Sparkline
                    data={series}
                    color="var(--inspect-accent)"
                    w={140}
                    h={22}
                    fill
                />
                <span className="inspect-num inspect-fn-pps">{fmtPkts(pps)} pps</span>
            </div>
        </div>
    );
};
