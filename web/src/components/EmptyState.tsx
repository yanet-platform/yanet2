import React from 'react';
import { Text } from '@gravity-ui/uikit';

export interface EmptyStateProps {
    message: string;
}

export const EmptyState: React.FC<EmptyStateProps> = ({ message }) => (
    <Text variant="body-1" color="secondary" style={{ display: 'block' }}>
        {message}
    </Text>
);

