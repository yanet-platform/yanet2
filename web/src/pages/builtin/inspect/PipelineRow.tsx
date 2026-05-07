import React from 'react';
import { FlowLine } from './FlowLine';
import { Sparkline } from './Sparkline';
import { fmtPkts } from './formatters';

export interface PipelineRowProps {
    name: string;
    functions: string[];
    pps: number;
    series: number[];
}

export const PipelineRow: React.FC<PipelineRowProps> = ({ name, functions, pps, series }) => {
    const flow = functions.length > 0 ? ['RX', ...functions, 'TX'] : [];

    return (
        <div className="inspect-pipe-row">
            <div className="inspect-pipe-head">
                <div className="inspect-pipe-id">
                    <span className="inspect-pipe-name inspect-mono">{name}</span>
                    <span className="inspect-pipe-fncount">{functions.length} fn</span>
                </div>
                <FlowLine flow={flow} />
                <div className="inspect-pipe-meta">
                    <Sparkline
                        data={series}
                        color="var(--inspect-accent)"
                        w={88}
                        h={20}
                        fill
                    />
                    <span className="inspect-pipe-pps inspect-num">
                        {fmtPkts(pps)}
                        <span className="inspect-pipe-unit">pps</span>
                    </span>
                </div>
            </div>
        </div>
    );
};
