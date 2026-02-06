import React, { useState, useCallback, useEffect, useMemo } from 'react';
import { TextInput, Box, Select } from '@gravity-ui/uikit';
import type { SelectOption } from '@gravity-ui/uikit';
import { FormDialog, FormField } from '../../../components';
import type { ModuleNodeData } from '../types';
import '../../FunctionsPage.css';

export interface ModuleEditorDialogProps {
    open: boolean;
    onClose: () => void;
    onConfirm: (data: ModuleNodeData) => void;
    initialData: ModuleNodeData;
    availableModuleTypes: string[];
    loadingModuleTypes?: boolean;
}

export const ModuleEditorDialog: React.FC<ModuleEditorDialogProps> = ({
    open,
    onClose,
    onConfirm,
    initialData,
    availableModuleTypes,
    loadingModuleTypes = false,
}) => {
    const [type, setType] = useState('');
    const [filterText, setFilterText] = useState('');
    const [name, setName] = useState('');
    
    useEffect(() => {
        if (open) {
            setType(initialData.type || '');
            setFilterText('');
            setName(initialData.name || '');
        }
    }, [open, initialData]);
    
    const handleConfirm = useCallback(() => {
        onConfirm({
            type: type.trim(),
            name: name.trim(),
        });
    }, [type, name, onConfirm]);

    // Build options: available module types + custom input option if needed
    const selectOptions = useMemo((): SelectOption[] => {
        const options: SelectOption[] = availableModuleTypes.map(t => ({
            value: t,
            content: t,
        }));

        // If there's filter text that doesn't match any existing type exactly,
        // add it as a "create new" option
        const trimmedFilter = filterText.trim();
        if (trimmedFilter && !availableModuleTypes.some(t => t === trimmedFilter)) {
            options.unshift({
                value: trimmedFilter,
                content: `Use "${trimmedFilter}"`,
            });
        }

        return options;
    }, [availableModuleTypes, filterText]);

    const handleSelectChange = useCallback((values: string[]) => {
        if (values.length > 0) {
            setType(values[0]);
        } else {
            setType('');
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
        return type ? [type] : [];
    }, [type]);
    
    return (
        <FormDialog
            open={open}
            onClose={onClose}
            onConfirm={handleConfirm}
            title="Edit Module"
            confirmText="Save"
            showCancel={false}
        >
            <Box className="module-editor-dialog__body">
                <FormField
                    label="Module Type"
                    hint="Select from available types or type a custom name."
                >
                    <Select
                        value={selectedValue}
                        onUpdate={handleSelectChange}
                        options={selectOptions}
                        filterable
                        filter={filterText}
                        onFilterChange={handleFilterChange}
                        filterOption={filterOption}
                        placeholder={loadingModuleTypes ? 'Loading module types...' : 'Select or type module type'}
                        disabled={loadingModuleTypes}
                        popupWidth="fit"
                        width="max"
                        hasClear
                    />
                </FormField>
                
                <FormField
                    label="Module Name"
                    hint="Instance name of the module"
                >
                    <TextInput
                        value={name}
                        onUpdate={setName}
                        placeholder="Enter module name"
                        className="module-editor-dialog__input"
                    />
                </FormField>
            </Box>
        </FormDialog>
    );
};
