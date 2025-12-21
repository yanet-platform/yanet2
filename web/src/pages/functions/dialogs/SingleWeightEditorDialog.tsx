import React, { useState, useCallback, useEffect, useRef } from 'react';
import { TextInput, Box } from '@gravity-ui/uikit';
import { FormDialog, FormField } from '../../../components';
import '../../FunctionsPage.css';

export interface ChainEditorResult {
    chainName: string;
    weight: string;
}

export interface SingleWeightEditorDialogProps {
    open: boolean;
    onClose: () => void;
    onConfirm: (result: ChainEditorResult) => void;
    edgeId: string;
    initialChainName: string;
    initialWeight: string;
    /** List of existing chain names for uniqueness validation (excluding current) */
    existingChainNames: string[];
}

export const SingleWeightEditorDialog: React.FC<SingleWeightEditorDialogProps> = ({
    open,
    onClose,
    onConfirm,
    initialChainName,
    initialWeight,
    existingChainNames,
}) => {
    const [chainName, setChainName] = useState(initialChainName);
    const [weight, setWeight] = useState(initialWeight);
    const [nameError, setNameError] = useState<string | undefined>();
    const [weightError, setWeightError] = useState<string | undefined>();
    const nameInputRef = useRef<HTMLInputElement>(null);
    
    useEffect(() => {
        if (open) {
            setChainName(initialChainName);
            setWeight(initialWeight);
            setNameError(undefined);
            setWeightError(undefined);
            // Focus name input after dialog opens
            setTimeout(() => {
                nameInputRef.current?.focus();
                nameInputRef.current?.select();
            }, 50);
        }
    }, [open, initialChainName, initialWeight]);
    
    const validateChainName = useCallback((name: string): string | undefined => {
        const trimmedName = name.trim();
        if (!trimmedName) {
            return 'Chain name is required';
        }
        if (existingChainNames.includes(trimmedName)) {
            return 'Chain name must be unique';
        }
        return undefined;
    }, [existingChainNames]);

    const validateWeight = useCallback((value: string): string | undefined => {
        const trimmed = value.trim();
        if (!trimmed) {
            return 'Weight is required';
        }
        if (!/^\d+$/.test(trimmed)) {
            return 'Weight must be a non-negative integer';
        }
        return undefined;
    }, []);
    
    const handleConfirm = useCallback(() => {
        const error = validateChainName(chainName);
        const weightValidation = validateWeight(weight);
        if (error || weightValidation) {
            setNameError(error);
            setWeightError(weightValidation);
            return;
        }
        onConfirm({
            chainName: chainName.trim(),
            weight,
        });
    }, [chainName, weight, validateChainName, onConfirm]);
    
    const handleChainNameChange = useCallback((value: string) => {
        setChainName(value);
        // Clear error when user starts typing
        if (nameError) {
            setNameError(undefined);
        }
    }, [nameError]);
    
    const handleWeightChange = useCallback((value: string) => {
        setWeight(value);
        if (weightError) {
            setWeightError(undefined);
        }
    }, [weightError]);

    return (
        <FormDialog
            open={open}
            onClose={onClose}
            onConfirm={handleConfirm}
            title="Edit Chain"
            confirmText="Save"
            showCancel={false}
            width="350px"
        >
            <Box className="single-weight-editor-dialog__body">
                <FormField
                    label="Chain Name"
                    required
                    hint="Unique identifier for this chain"
                >
                    <TextInput
                        controlRef={nameInputRef}
                        value={chainName}
                        onUpdate={handleChainNameChange}
                        placeholder="Enter chain name"
                        className="single-weight-editor-dialog__input"
                        validationState={nameError ? 'invalid' : undefined}
                        errorMessage={nameError}
                    />
                </FormField>
                
                <FormField
                    label="Weight"
                    hint="Numeric weight for load balancing"
                >
                    <TextInput
                        value={weight}
                        onUpdate={handleWeightChange}
                        placeholder="Weight"
                        type="number"
                        className="single-weight-editor-dialog__input"
                        validationState={weightError ? 'invalid' : undefined}
                        errorMessage={weightError}
                    />
                </FormField>
            </Box>
        </FormDialog>
    );
};
