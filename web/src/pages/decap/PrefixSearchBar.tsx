import React from 'react';
import { Box, TextInput, Loader, Label, Text, Button } from '@gravity-ui/uikit';
import { SEARCH_BAR_HEIGHT } from './constants';

export interface PrefixSearchBarProps {
    searchQuery: string;
    onSearchChange: (value: string) => void;
    isSearching: boolean;
    statsText: string;
    selectedText: string | null;
    onClearSelection?: () => void;
    onAddPrefix: () => void;
}

export const PrefixSearchBar: React.FC<PrefixSearchBarProps> = ({
    searchQuery,
    onSearchChange,
    isSearching,
    statsText,
    selectedText,
    onClearSelection,
    onAddPrefix,
}) => {
    return (
        <Box style={{ display: 'flex', alignItems: 'center', gap: 16, height: SEARCH_BAR_HEIGHT, flexShrink: 0 }}>
            <Box style={{ width: 300 }}>
                <TextInput
                    placeholder="Search by prefix..."
                    value={searchQuery}
                    onUpdate={onSearchChange}
                    size="m"
                    hasClear
                />
            </Box>
            <Box style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                {isSearching && <Loader size="s" />}
                <Label theme="info" size="m">{statsText}</Label>
                {selectedText && (
                    <>
                        <Label theme="warning" size="m">{selectedText}</Label>
                        {onClearSelection && (
                            <Text
                                variant="body-1"
                                color="link"
                                style={{ cursor: 'pointer' }}
                                onClick={onClearSelection}
                            >
                                Clear
                            </Text>
                        )}
                    </>
                )}
            </Box>
            <Box style={{ flex: 1 }} />
            <Button view="action" onClick={onAddPrefix}>
                Add Prefix
            </Button>
        </Box>
    );
};

