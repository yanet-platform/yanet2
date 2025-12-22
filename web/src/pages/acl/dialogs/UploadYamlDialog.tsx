import React, { useState, useCallback, useEffect, useRef } from 'react';
import { Dialog, Box, Text, TextInput, Loader } from '@gravity-ui/uikit';
import { FileArrowUp } from '@gravity-ui/icons';
import type { UploadYamlDialogProps } from '../types';
import type { Rule } from '../../../api/acl';
import { parseYamlConfig } from '../yamlParser';
import './UploadYamlDialog.css';

export const UploadYamlDialog: React.FC<UploadYamlDialogProps> = ({
    open,
    onClose,
    onConfirm,
    existingConfigs,
}) => {
    const [configName, setConfigName] = useState('');
    const [file, setFile] = useState<File | null>(null);
    const [parsedRules, setParsedRules] = useState<Rule[] | null>(null);
    const [parseError, setParseError] = useState<string | null>(null);
    const [configNameError, setConfigNameError] = useState<string | undefined>();
    const [isReading, setIsReading] = useState(false);
    const fileInputRef = useRef<HTMLInputElement>(null);

    // Reset form when dialog opens/closes
    useEffect(() => {
        if (open) {
            setConfigName('');
            setFile(null);
            setParsedRules(null);
            setParseError(null);
            setConfigNameError(undefined);
            setIsReading(false);
        }
    }, [open]);

    const handleFileSelect = useCallback((event: React.ChangeEvent<HTMLInputElement>) => {
        const selectedFile = event.target.files?.[0];
        if (!selectedFile) return;

        setFile(selectedFile);
        setParsedRules(null);
        setParseError(null);
        setIsReading(true);

        // Read and parse file
        const reader = new FileReader();
        reader.onload = (e) => {
            try {
                const content = e.target?.result as string;
                const rules = parseYamlConfig(content);
                setParsedRules(rules);
                setParseError(null);
            } catch (err) {
                setParseError(err instanceof Error ? err.message : 'Failed to parse YAML file');
                setParsedRules(null);
            } finally {
                setIsReading(false);
            }
        };
        reader.onerror = () => {
            setParseError('Failed to read file');
            setParsedRules(null);
            setIsReading(false);
        };
        reader.readAsText(selectedFile);
    }, []);

    const handleBrowseClick = useCallback(() => {
        fileInputRef.current?.click();
    }, []);

    const validateConfigName = useCallback((name: string): string | undefined => {
        const trimmed = name.trim();
        if (!trimmed) {
            return 'Config name is required';
        }
        return undefined;
    }, []);

    const handleConfigNameChange = useCallback((value: string) => {
        setConfigName(value);
        setConfigNameError(validateConfigName(value));
    }, [validateConfigName]);

    const handleConfirm = useCallback(() => {
        const nameError = validateConfigName(configName);
        if (nameError) {
            setConfigNameError(nameError);
            return;
        }

        if (!parsedRules) {
            return;
        }

        onConfirm(configName.trim(), parsedRules);
    }, [configName, parsedRules, validateConfigName, onConfirm]);

    const isExistingConfig = existingConfigs.includes(configName.trim());
    const canConfirm = configName.trim() && parsedRules && !configNameError && !isReading;

    return (
        <Dialog open={open} onClose={onClose}>
            <Dialog.Header caption="Create ACL Configuration" />
            <Dialog.Body>
                <Box className="upload-yaml-dialog__body">
                    {/* Config name input */}
                    <Box>
                        <Text variant="body-2" className="upload-yaml-dialog__label">
                            Config Name <Text color="danger">*</Text>
                        </Text>
                        <TextInput
                            value={configName}
                            onUpdate={handleConfigNameChange}
                            placeholder="Enter config name"
                            validationState={configNameError ? 'invalid' : undefined}
                            errorMessage={configNameError}
                            autoFocus
                        />
                        {isExistingConfig && !configNameError && (
                            <Text variant="caption-2" color="warning" className="upload-yaml-dialog__warning">
                                This config already exists. Uploading will replace its rules.
                            </Text>
                        )}
                    </Box>

                    {/* File upload */}
                    <Box>
                        <Text variant="body-2" className="upload-yaml-dialog__label">
                            YAML File <Text color="danger">*</Text>
                        </Text>
                        <input
                            ref={fileInputRef}
                            type="file"
                            accept=".yaml,.yml"
                            onChange={handleFileSelect}
                            style={{ display: 'none' }}
                        />
                        <div
                            className="upload-yaml-dialog__dropzone"
                            onClick={handleBrowseClick}
                            role="button"
                            tabIndex={0}
                            onKeyDown={(e) => {
                                if (e.key === 'Enter' || e.key === ' ') {
                                    handleBrowseClick();
                                }
                            }}
                        >
                            {isReading ? (
                                <>
                                    <Loader size="m" />
                                    <Text variant="body-2" color="secondary">
                                        Reading file...
                                    </Text>
                                </>
                            ) : (
                                <>
                                    <FileArrowUp width={32} height={32} className="upload-yaml-dialog__dropzone-icon" />
                                    {file ? (
                                        <Text variant="body-2">{file.name}</Text>
                                    ) : (
                                        <Text variant="body-2" color="secondary">
                                            Click to select YAML file
                                        </Text>
                                    )}
                                </>
                            )}
                        </div>
                    </Box>

                    {/* Parse error */}
                    {parseError && (
                        <Box className="upload-yaml-dialog__error">
                            <Text variant="body-2" color="danger">
                                {parseError}
                            </Text>
                        </Box>
                    )}

                    {/* Success message */}
                    {parsedRules && !isReading && (
                        <Box className="upload-yaml-dialog__success">
                            <Text variant="body-2" color="positive">
                                Successfully parsed {parsedRules.length} rule{parsedRules.length !== 1 ? 's' : ''}
                            </Text>
                        </Box>
                    )}
                </Box>
            </Dialog.Body>
            <Dialog.Footer
                onClickButtonCancel={onClose}
                onClickButtonApply={handleConfirm}
                textButtonApply={isExistingConfig ? 'Replace Config' : 'Create Config'}
                textButtonCancel="Cancel"
                propsButtonApply={{
                    disabled: !canConfirm,
                    view: isExistingConfig ? 'outlined-warning' : 'action',
                }}
            />
        </Dialog>
    );
};
