import React from 'react';

export interface InspectCardProps {
    title?: string;
    count?: number;
    right?: React.ReactNode;
    children: React.ReactNode;
}

export const InspectCard: React.FC<InspectCardProps> = ({ title, count, right, children }) => {
    const showHeader = title !== undefined || right !== undefined;
    return (
        <section className="inspect-card">
            {showHeader && (
                <header className="inspect-card-head">
                    <div className="inspect-card-title-group">
                        {title && <h3 className="inspect-card-title">{title}</h3>}
                        {count !== undefined && (
                            <span className="inspect-card-count inspect-num">{count}</span>
                        )}
                    </div>
                    {right && <div className="inspect-card-right">{right}</div>}
                </header>
            )}
            <div className="inspect-card-body">{children}</div>
        </section>
    );
};
