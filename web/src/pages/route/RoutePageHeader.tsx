import React from 'react';
import { Box, Text, Flex, Button, Select, Switch, Label } from '@gravity-ui/uikit';
import type { RoutePageHeaderProps } from './types';
import { MOCK_CONFIGS } from './mockData';
import './route.css';

const mockOptions = Object.entries(MOCK_CONFIGS).map(([key, value]) => ({
    value: key,
    content: value.label,
}));

export const RoutePageHeader: React.FC<RoutePageHeaderProps> = ({
    onAddRoute,
    onDeleteRoute,
    onFlush,
    isDeleteDisabled,
    isFlushDisabled,
    mockEnabled,
    onMockToggle,
    mockSize,
    onMockSizeChange,
}) => (
    <Flex className="route-page-header">
        <Text variant="header-1">Route</Text>
        <Box className="route-page-header__spacer" />
        <Box className="route-page-header__actions">
            {/* Mock mode controls */}
            <Box className="route-page-header__mock-controls">
                <Switch
                    checked={mockEnabled}
                    onUpdate={onMockToggle}
                    size="m"
                />
                <Label size="m">Mock Mode</Label>
                {mockEnabled && (
                    <Select
                        value={[mockSize]}
                        onUpdate={(values) => onMockSizeChange(values[0])}
                        options={mockOptions}
                        size="m"
                        width={140}
                    />
                )}
            </Box>
            <Button view="action" onClick={onAddRoute} disabled={mockEnabled}>
                Add Route
            </Button>
            <Button
                view="outlined-danger"
                onClick={onDeleteRoute}
                disabled={isDeleteDisabled || mockEnabled}
            >
                Delete Route
            </Button>
            <Button
                view="outlined"
                onClick={onFlush}
                disabled={isFlushDisabled || mockEnabled}
            >
                Flush RIB → FIB
            </Button>
        </Box>
    </Flex>
);
