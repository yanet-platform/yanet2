import React from 'react';
import { Text } from '@gravity-ui/uikit';
import './common.css';

export interface EmptyStateProps {
    message: string;
}

export const EmptyState: React.FC<EmptyStateProps> = ({ message }) => (
    <Text variant="body-1" color="secondary" className="empty-state">
        {message}
    </Text>
);
