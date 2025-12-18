import React from 'react';
import { Box, TextInput, Label, Text, Button } from '@gravity-ui/uikit';
import { Stop, TrashBin } from '@gravity-ui/icons';
import { SEARCH_BAR_HEIGHT } from './constants';

export interface PacketSearchBarProps {
    searchQuery: string;
    onSearchChange: (value: string) => void;
    statsText: string;
    isCapturing: boolean;
    configName: string | null;
    onStopCapture: () => void;
    onClearPackets: () => void;
    canClear: boolean;
}

export const PacketSearchBar: React.FC<PacketSearchBarProps> = ({
    searchQuery,
    onSearchChange,
    statsText,
    isCapturing,
    configName,
    onStopCapture,
    onClearPackets,
    canClear,
}) => {
    return (
        <Box style={{ display: 'flex', alignItems: 'center', gap: 16, height: SEARCH_BAR_HEIGHT, flexShrink: 0 }}>
            <Box style={{ width: 350 }}>
                <TextInput
                    placeholder="Filter by IP, port, protocol..."
                    value={searchQuery}
                    onUpdate={onSearchChange}
                    size="m"
                    hasClear
                />
            </Box>
            <Box style={{ display: 'flex', alignItems: 'center', gap: 8, flex: 1 }}>
                <Label theme="info" size="m">{statsText}</Label>
                {configName && (
                    <Text variant="body-1" color="secondary">
                        Capture: {configName}
                    </Text>
                )}
                {isCapturing && (
                    <Label theme="success" size="s">LIVE</Label>
                )}
            </Box>
            <Box style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                <Button
                    view="flat"
                    size="s"
                    onClick={onClearPackets}
                    disabled={!canClear}
                >
                    <Button.Icon>
                        <TrashBin />
                    </Button.Icon>
                    Clear
                </Button>
                {isCapturing && (
                    <Button
                        view="outlined-danger"
                        size="s"
                        onClick={onStopCapture}
                    >
                        <Button.Icon>
                            <Stop />
                        </Button.Icon>
                        Stop
                    </Button>
                )}
            </Box>
        </Box>
    );
};

