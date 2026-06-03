import React from 'react';
import { Button } from '@gravity-ui/uikit';

export interface EmptyPagePlaceholderProps {
    message: string;
    actionLabel: string;
    onAction: () => void;
}

/** Full-page placeholder shown when a module/operator page has no configs yet. */
export const EmptyPagePlaceholder: React.FC<EmptyPagePlaceholderProps> = ({ message, actionLabel, onAction }) => (
    <div className="yn-empty-page">
        <div className="yn-empty-page__message">{message}</div>
        <Button view="action" onClick={onAction}>{actionLabel}</Button>
    </div>
);
