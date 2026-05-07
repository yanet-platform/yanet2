import React from 'react';

export interface FlowLineProps {
    flow: string[];
}

const ArrowGlyph: React.FC = () => (
    <span className="inspect-flow-arrow" aria-hidden>
        <svg width="10" height="10" viewBox="0 0 10 10">
            <path
                d="M2 5h6M6 2l2 3-2 3"
                fill="none"
                stroke="currentColor"
                strokeWidth="1.2"
                strokeLinecap="round"
                strokeLinejoin="round"
            />
        </svg>
    </span>
);

export const FlowLine: React.FC<FlowLineProps> = ({ flow }) => {
    if (!flow || flow.length === 0) {
        return <span className="inspect-empty">No functions</span>;
    }

    const els: React.ReactNode[] = [];
    flow.forEach((tok, i) => {
        const isIo = tok === 'RX' || tok === 'TX';
        const cls = isIo
            ? 'inspect-flow-tok inspect-flow-tok--io'
            : 'inspect-flow-tok';
        els.push(
            <span key={`${i}:t`} className={cls}>
                {tok}
            </span>,
        );
        if (i < flow.length - 1) {
            els.push(<ArrowGlyph key={`${i}:a`} />);
        }
    });

    return (
        <div className="inspect-flow-wrap">
            <div className="inspect-flow">{els}</div>
        </div>
    );
};
