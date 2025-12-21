import React, { useState, useCallback, useEffect, useMemo } from 'react';
import { Select, Box } from '@gravity-ui/uikit';
import type { SelectOption } from '@gravity-ui/uikit';
import { FormDialog, FormField } from '../../../components';
import type { FunctionRefNodeData } from '../types';
import type { FunctionId } from '../../../api/common';
import '../pipelines.css';

export interface FunctionRefEditorDialogProps {
    open: boolean;
    onClose: () => void;
    onConfirm: (data: FunctionRefNodeData) => void;
    initialData: FunctionRefNodeData;
    availableFunctions: FunctionId[];
    loadingFunctions?: boolean;
}

export const FunctionRefEditorDialog: React.FC<FunctionRefEditorDialogProps> = ({
    open,
    onClose,
    onConfirm,
    initialData,
    availableFunctions,
    loadingFunctions = false,
}) => {
    const [functionName, setFunctionName] = useState('');
    const [filterText, setFilterText] = useState('');

    useEffect(() => {
        if (open) {
            setFunctionName(initialData.functionName || '');
            setFilterText('');
        }
    }, [open, initialData]);

    const handleConfirm = useCallback(() => {
        onConfirm({
            functionName: functionName.trim(),
        });
    }, [functionName, onConfirm]);

    // Build options: available functions + custom input option if needed
    const selectOptions = useMemo((): SelectOption[] => {
        const options: SelectOption[] = availableFunctions.map(f => ({
            value: f.name || '',
            content: f.name || '',
        }));

        // If there's filter text that doesn't match any existing function exactly,
        // add it as a "create new" option
        const trimmedFilter = filterText.trim();
        if (trimmedFilter && !availableFunctions.some(f => f.name === trimmedFilter)) {
            options.unshift({
                value: trimmedFilter,
                content: `Use "${trimmedFilter}"`,
            });
        }

        return options;
    }, [availableFunctions, filterText]);

    const handleSelectChange = useCallback((values: string[]) => {
        if (values.length > 0) {
            setFunctionName(values[0]);
        } else {
            setFunctionName('');
        }
    }, []);

    const handleFilterChange = useCallback((filter: string) => {
        setFilterText(filter);
    }, []);

    // Custom filter: show all options that contain the filter text
    const filterOption = useCallback((option: SelectOption, filter: string): boolean => {
        const optionText = String(option.content || option.value).toLowerCase();
        const filterLower = filter.toLowerCase().trim();

        // Always show the "Use ..." option for custom input
        if (optionText.startsWith('use "')) {
            return true;
        }

        return optionText.includes(filterLower);
    }, []);

    // Selected value for the Select component
    const selectedValue = useMemo(() => {
        return functionName ? [functionName] : [];
    }, [functionName]);

    return (
        <FormDialog
            open={open}
            onClose={onClose}
            onConfirm={handleConfirm}
            title="Edit Function Reference"
            confirmText="Save"
            showCancel={false}
        >
            <Box className="function-ref-editor-dialog__body">
                <FormField
                    label="Function Name"
                    hint="Select from available functions or type a custom name."
                >
                    <Select
                        value={selectedValue}
                        onUpdate={handleSelectChange}
                        options={selectOptions}
                        filterable
                        filter={filterText}
                        onFilterChange={handleFilterChange}
                        filterOption={filterOption}
                        placeholder={loadingFunctions ? 'Loading functions...' : 'Select or type function name'}
                        disabled={loadingFunctions}
                        popupWidth="fit"
                        width="max"
                        hasClear
                    />
                </FormField>
            </Box>
        </FormDialog>
    );
};
