import React from 'react';
import { Box, Text } from '@gravity-ui/uikit';
import type { RouteListItemProps } from './types';
import { ipAddressToString } from '../../../utils/netip';
import './route.scss';

export const RouteListItem: React.FC<RouteListItemProps> = ({ route }) => {
    const nextHop = ipAddressToString(route.next_hop);
    const peer = ipAddressToString(route.peer);
    return (
        <Box className="route-list-item">
            <Text variant="body-1">
                {route.prefix || '-'} → {nextHop || '-'}
                {peer && ` (peer: ${peer})`}
            </Text>
        </Box>
    );
};
