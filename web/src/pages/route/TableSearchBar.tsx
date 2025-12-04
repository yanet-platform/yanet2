import React from 'react';
import { Box, TextInput, Loader, Label, Text } from '@gravity-ui/uikit';
import { SEARCH_BAR_HEIGHT } from './constants';

export interface TableSearchBarProps {
    searchQuery: string;
    onSearchChange: (value: string) => void;
    isSearching: boolean;
    statsText: string;
    selectedText: string | null;
    onClearSelection?: () => void;
    helperText?: string;
    placeholder?: string;
}

export const TableSearchBar: React.FC<TableSearchBarProps> = ({
    searchQuery,
    onSearchChange,
    isSearching,
    statsText,
    selectedText,
    onClearSelection,
    helperText,
    placeholder = 'Search by prefix, nexthop, or peer...',
}) => {
    return (
        <Box style={{ display: 'flex', alignItems: 'center', gap: 16, height: SEARCH_BAR_HEIGHT, flexShrink: 0 }}>
            <Box style={{ width: 350 }}>
                <TextInput
                    placeholder={placeholder}
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
                {helperText && (
                    <Text variant="body-2" color="secondary">
                        {helperText}
                    </Text>
                )}
            </Box>
        </Box>
    );
};
