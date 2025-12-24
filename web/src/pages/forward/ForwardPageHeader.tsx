import React from 'react';
import { Box, Text, Flex, Button } from '@gravity-ui/uikit';
import './forward.css';

export interface ForwardPageHeaderProps {
    onAddRule: () => void;
    onDeleteRules: () => void;
    isDeleteDisabled: boolean;
}

export const ForwardPageHeader: React.FC<ForwardPageHeaderProps> = ({
    onAddRule,
    onDeleteRules,
    isDeleteDisabled,
}) => (
    <Flex className="forward-page-header">
        <Text variant="header-1">Forward</Text>
        <Box className="forward-page-header__spacer" />
        <Box className="forward-page-header__actions">
            <Button view="action" onClick={onAddRule}>
                Add Rule
            </Button>
            <Button
                view="outlined-danger"
                onClick={onDeleteRules}
                disabled={isDeleteDisabled}
            >
                Delete Rules
            </Button>
        </Box>
    </Flex>
);
