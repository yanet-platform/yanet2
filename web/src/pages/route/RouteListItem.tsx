import React from 'react';
import { Box, Text } from '@gravity-ui/uikit';
import type { RouteListItemProps } from './types';

export const RouteListItem: React.FC<RouteListItemProps> = ({ route }) => (
    <Box style={{ padding: '4px 0' }}>
        <Text variant="body-1">
            {route.prefix || '-'} → {route.nextHop || '-'}
            {route.peer && ` (peer: ${route.peer})`}
        </Text>
    </Box>
);
