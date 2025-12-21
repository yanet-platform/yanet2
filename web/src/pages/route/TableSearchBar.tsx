import React from 'react';
import { Box, TextInput, Loader, Label, Text } from '@gravity-ui/uikit';
import { SEARCH_BAR_HEIGHT } from './constants';
import './route.css';

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
        <Box className="route-search-bar" style={{ height: SEARCH_BAR_HEIGHT }}>
            <Box className="route-search-bar__input">
                <TextInput
                    placeholder={placeholder}
                    value={searchQuery}
                    onUpdate={onSearchChange}
                    size="m"
                    hasClear
                />
            </Box>
            <Box className="route-search-bar__stats">
                {isSearching && <Loader size="s" />}
                <Label theme="info" size="m">{statsText}</Label>
                {selectedText && (
                    <>
                        <Label theme="warning" size="m">{selectedText}</Label>
                        {onClearSelection && (
                            <Text
                                variant="body-1"
                                color="link"
                                className="route-search-bar__clear"
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
