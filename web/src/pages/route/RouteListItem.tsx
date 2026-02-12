import React from 'react';
import { Box, Text } from '@gravity-ui/uikit';
import type { RouteListItemProps } from './types';
import './route.scss';

export const RouteListItem: React.FC<RouteListItemProps> = ({ route }) => (
    <Box className="route-list-item">
        <Text variant="body-1">
            {route.prefix || '-'} → {route.next_hop || '-'}
            {route.peer && ` (peer: ${route.peer})`}
        </Text>
    </Box>
);
