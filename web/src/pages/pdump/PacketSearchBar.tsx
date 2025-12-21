import React from 'react';
import { Box, TextInput, Label, Text, Button } from '@gravity-ui/uikit';
import { Stop, TrashBin } from '@gravity-ui/icons';
import { SEARCH_BAR_HEIGHT } from './constants';
import './pdump.css';

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
        <Box className="packet-search-bar" style={{ height: SEARCH_BAR_HEIGHT }}>
            <Box className="packet-search-bar__input">
                <TextInput
                    placeholder="Filter by IP, port, protocol..."
                    value={searchQuery}
                    onUpdate={onSearchChange}
                    size="m"
                    hasClear
                />
            </Box>
            <Box className="packet-search-bar__info">
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
            <Box className="packet-search-bar__actions">
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
