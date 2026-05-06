import React from 'react';
import { Flex, Text } from '@gravity-ui/uikit';
import './common.scss';

export interface EmptyStateProps {
    message: string;
    /** When true, renders inline (no flex centering / no fill). For
     *  contexts like inside a list cell. */
    compact?: boolean;
}

export const EmptyState: React.FC<EmptyStateProps> = ({ message, compact = false }) => {
    if (compact) {
        return (
            <Text variant="body-1" color="secondary" className="empty-state empty-state--compact">
                {message}
            </Text>
        );
    }
    return (
        <Flex
            alignItems="center"
            justifyContent="center"
            className="empty-state"
        >
            <Text variant="body-1" color="secondary">
                {message}
            </Text>
        </Flex>
    );
};
