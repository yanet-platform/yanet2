import React from 'react';
import { Box, Text, Flex, Button } from '@gravity-ui/uikit';
import './decap.css';

export interface DecapPageHeaderProps {
    onAddPrefix: () => void;
    onDeletePrefixes: () => void;
    isDeleteDisabled: boolean;
}

export const DecapPageHeader: React.FC<DecapPageHeaderProps> = ({
    onAddPrefix,
    onDeletePrefixes,
    isDeleteDisabled,
}) => (
    <Flex className="decap-page-header">
        <Text variant="header-1">Decap</Text>
        <Box className="decap-page-header__spacer" />
        <Box className="decap-page-header__actions">
            <Button view="action" onClick={onAddPrefix}>
                Add Prefix
            </Button>
            <Button
                view="outlined-danger"
                onClick={onDeletePrefixes}
                disabled={isDeleteDisabled}
            >
                Delete Prefixes
            </Button>
        </Box>
    </Flex>
);
