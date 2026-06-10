import React from 'react';
import { Button } from '@gravity-ui/uikit';

export interface EmptyPagePlaceholderProps {
    message: string;
    actionLabel: string;
    onAction: () => void;
    /** When true the action button is rendered disabled. Omit or pass false to keep it enabled. */
    actionDisabled?: boolean;
}

/** Full-page placeholder shown when a module/operator page has no configs yet. */
export const EmptyPagePlaceholder: React.FC<EmptyPagePlaceholderProps> = ({ message, actionLabel, onAction, actionDisabled }) => (
    <div className="yn-empty-page">
        <div className="yn-empty-page__message">{message}</div>
        <Button view="action" onClick={onAction} disabled={actionDisabled}>{actionLabel}</Button>
    </div>
);
