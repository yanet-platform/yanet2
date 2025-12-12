import React from 'react';
import { Box, Text, Flex, Button } from '@gravity-ui/uikit';

export interface DecapPageHeaderProps {
    onAddConfig: () => void;
    onDeletePrefixes: () => void;
    isDeleteDisabled: boolean;
}

export const DecapPageHeader: React.FC<DecapPageHeaderProps> = ({
    onAddConfig,
    onDeletePrefixes,
    isDeleteDisabled,
}) => (
    <Flex style={{ width: '100%', alignItems: 'center' }}>
        <Text variant="header-1">Decap</Text>
        <Box style={{ flex: 1 }} />
        <Box style={{ display: 'flex', gap: '16px', alignItems: 'center' }}>
            <Button view="action" onClick={onAddConfig}>
                Add Config
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
