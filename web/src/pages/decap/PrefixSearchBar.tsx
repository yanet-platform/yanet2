import React from 'react';
import { Box, TextInput, Loader, Label, Text, Button } from '@gravity-ui/uikit';
import { SEARCH_BAR_HEIGHT } from './constants';
import './decap.css';

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
        <Box className="prefix-search-bar" style={{ height: SEARCH_BAR_HEIGHT }}>
            <Box className="prefix-search-bar__input">
                <TextInput
                    placeholder="Search by prefix..."
                    value={searchQuery}
                    onUpdate={onSearchChange}
                    size="m"
                    hasClear
                />
            </Box>
            <Box className="prefix-search-bar__stats">
                {isSearching && <Loader size="s" />}
                <Label theme="info" size="m">{statsText}</Label>
                {selectedText && (
                    <>
                        <Label theme="warning" size="m">{selectedText}</Label>
                        {onClearSelection && (
                            <Text
                                variant="body-1"
                                color="link"
                                className="prefix-search-bar__clear"
                                onClick={onClearSelection}
                            >
                                Clear
                            </Text>
                        )}
                    </>
                )}
            </Box>
            <Box className="prefix-search-bar__spacer" />
            <Button view="action" onClick={onAddPrefix}>
                Add Prefix
            </Button>
        </Box>
    );
};
