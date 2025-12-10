import React from 'react';
import { Flex, Text } from '@gravity-ui/uikit';

export interface PageHeaderProps {
    /** Page title displayed in header */
    title: string;
    /** Action buttons or other content to display on the right side */
    actions?: React.ReactNode;
}

/**
 * Reusable page header component with title and optional actions
 */
export const PageHeader: React.FC<PageHeaderProps> = ({ title, actions }) => {
    return (
        <Flex
            alignItems="center"
            justifyContent="space-between"
            style={{ width: '100%' }}
        >
            <Text variant="header-1">{title}</Text>
            {actions && (
                <Flex gap={2} alignItems="center">
                    {actions}
                </Flex>
            )}
        </Flex>
    );
};
