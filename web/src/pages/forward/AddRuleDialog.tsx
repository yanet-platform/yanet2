import React, { useState, useCallback, useEffect } from 'react';
import { Box, Dialog, TextInput, Select } from '@gravity-ui/uikit';
import { FormField } from '../../components';
import type { AddRuleDialogProps } from './types';
import type { Rule } from '../../api/forward';
import { ForwardMode } from '../../api/forward';
import { parseDevices, parseVlanRanges, parsePrefixesToIPNets } from './hooks';
import './forward.css';

const MODE_OPTIONS = [
    { value: 'NONE', content: 'NONE' },
    { value: 'IN', content: 'IN' },
    { value: 'OUT', content: 'OUT' },
];

const modeStringToEnum = (mode: string): ForwardMode => {
    switch (mode) {
        case 'IN': return ForwardMode.IN;
        case 'OUT': return ForwardMode.OUT;
        default: return ForwardMode.NONE;
    }
};

export const AddRuleDialog: React.FC<AddRuleDialogProps> = ({
    open,
    onClose,
    onConfirm,
    existingConfigs,
    currentConfig,
}) => {
    const [configName, setConfigName] = useState<string>(currentConfig || '');
    const [target, setTarget] = useState<string>('');
    const [mode, setMode] = useState<string[]>(['NONE']);
    const [counter, setCounter] = useState<string>('');
    const [devices, setDevices] = useState<string>('');
    const [vlans, setVlans] = useState<string>('');
    const [srcs, setSrcs] = useState<string>('');
    const [dsts, setDsts] = useState<string>('');
    const [isSubmitting, setIsSubmitting] = useState<boolean>(false);

    // Reset form when dialog opens
    useEffect(() => {
        if (open) {
            setConfigName(currentConfig || existingConfigs[0] || '');
            setTarget('');
            setMode(['NONE']);
            setCounter('');
            setDevices('');
            setVlans('');
            setSrcs('');
            setDsts('');
            setIsSubmitting(false);
        }
    }, [open, currentConfig, existingConfigs]);

    const handleClose = useCallback(() => {
        onClose();
    }, [onClose]);

    const validateForm = useCallback((): string | undefined => {
        if (!configName.trim()) {
            return 'Config name is required';
        }
        if (!target.trim()) {
            return 'Target is required';
        }
        return undefined;
    }, [configName, target]);

    const handleConfirm = useCallback(async () => {
        const error = validateForm();
        if (error) return;

        setIsSubmitting(true);
        try {
            const rule: Rule = {
                action: {
                    target: target.trim(),
                    mode: modeStringToEnum(mode[0]),
                    counter: counter.trim() || undefined,
                },
                devices: parseDevices(devices),
                vlanRanges: parseVlanRanges(vlans),
                srcs: parsePrefixesToIPNets(srcs),
                dsts: parsePrefixesToIPNets(dsts),
            };

            await onConfirm(configName.trim(), rule);
            handleClose();
        } finally {
            setIsSubmitting(false);
        }
    }, [configName, target, mode, counter, devices, vlans, srcs, dsts, validateForm, onConfirm, handleClose]);

    const formError = validateForm();
    const isConfigEmpty = configName.trim().length === 0;
    const isTargetEmpty = target.trim().length === 0;
    const canSubmit = !formError && !isSubmitting;

    // Handle Ctrl+Enter / Cmd+Enter
    useEffect(() => {
        if (!open) return;

        const handleKeyDown = (e: KeyboardEvent) => {
            if ((e.ctrlKey || e.metaKey) && e.key === 'Enter' && canSubmit) {
                e.preventDefault();
                handleConfirm();
            }
        };

        document.addEventListener('keydown', handleKeyDown);
        return () => document.removeEventListener('keydown', handleKeyDown);
    }, [open, canSubmit, handleConfirm]);

    return (
        <Dialog open={open} onClose={handleClose}>
            <Dialog.Header caption="Add Forward Rule" />
            <Dialog.Body>
                <Box className="forward-dialog__body">
                    <FormField label="Config Name" required>
                        <TextInput
                            value={configName}
                            onUpdate={setConfigName}
                            placeholder="Enter config name"
                            className="forward-dialog__text-input"
                            validationState={!isConfigEmpty && !configName.trim() ? 'invalid' : undefined}
                        />
                    </FormField>

                    <FormField label="Target" required hint="The target to forward traffic to">
                        <TextInput
                            value={target}
                            onUpdate={setTarget}
                            placeholder="e.g., eth0"
                            className="forward-dialog__text-input"
                            validationState={!isTargetEmpty && !target.trim() ? 'invalid' : undefined}
                            autoFocus
                        />
                    </FormField>

                    <FormField label="Mode" hint="Forward direction mode">
                        <Select
                            value={mode}
                            onUpdate={setMode}
                            options={MODE_OPTIONS}
                            className="forward-dialog__select"
                        />
                    </FormField>

                    <FormField label="Counter" hint="Optional counter name">
                        <TextInput
                            value={counter}
                            onUpdate={setCounter}
                            placeholder="e.g., my_counter"
                            className="forward-dialog__text-input"
                        />
                    </FormField>

                    <FormField label="Devices" hint="Comma-separated device names">
                        <TextInput
                            value={devices}
                            onUpdate={setDevices}
                            placeholder="e.g., eth0, eth1"
                            className="forward-dialog__text-input"
                        />
                    </FormField>

                    <FormField label="VLAN Ranges" hint="Format: 1-100, 200, 300-400">
                        <TextInput
                            value={vlans}
                            onUpdate={setVlans}
                            placeholder="e.g., 1-100, 200"
                            className="forward-dialog__text-input"
                        />
                    </FormField>

                    <FormField label="Sources" hint="Comma-separated CIDR prefixes">
                        <TextInput
                            value={srcs}
                            onUpdate={setSrcs}
                            placeholder="e.g., 10.0.0.0/8, 192.168.0.0/16"
                            className="forward-dialog__text-input"
                        />
                    </FormField>

                    <FormField label="Destinations" hint="Comma-separated CIDR prefixes">
                        <TextInput
                            value={dsts}
                            onUpdate={setDsts}
                            placeholder="e.g., 10.0.0.0/8, 192.168.0.0/16"
                            className="forward-dialog__text-input"
                        />
                    </FormField>
                </Box>
            </Dialog.Body>
            <Dialog.Footer
                onClickButtonApply={handleConfirm}
                onClickButtonCancel={handleClose}
                textButtonApply="Add Rule"
                textButtonCancel="Cancel"
                propsButtonApply={{
                    view: 'action' as const,
                    disabled: !canSubmit,
                    loading: isSubmitting,
                }}
            />
        </Dialog>
    );
};
