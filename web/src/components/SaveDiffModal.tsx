import React, { useMemo, useState } from 'react';
import { Dialog, Flex, Text } from '@gravity-ui/uikit';
import { diffLines } from 'diff';
import { SideBySideDiff } from './SideBySideDiff';

export interface SaveDiffModalProps {
    /** Config name shown in the dialog caption. */
    configName: string;
    /** Server-side YAML string (left / "before" pane). */
    beforeYaml: string;
    /** Draft YAML string (right / "after" pane). */
    afterYaml: string;
    /** Optional extra warning rendered above the diff (e.g. validation count). */
    warning?: React.ReactNode;
    /** Label for the apply button. Defaults to "Apply". */
    applyLabel?: string;
    onClose: () => void;
    /** Called when the user confirms. Should throw on failure so the error banner fires. */
    onApply: () => Promise<void>;
}

/**
 * Generic modal showing a side-by-side YAML diff with an Apply button.
 * Callers supply pre-rendered YAML strings; this component owns diff computation
 * and the Gravity Dialog chrome.
 */
export const SaveDiffModal: React.FC<SaveDiffModalProps> = ({
    configName,
    beforeYaml,
    afterYaml,
    warning,
    applyLabel = 'Apply',
    onClose,
    onApply,
}) => {
    const [applying, setApplying] = useState(false);
    const [applyError, setApplyError] = useState<string | null>(null);

    const changes = useMemo(() => diffLines(beforeYaml, afterYaml), [beforeYaml, afterYaml]);

    const handleApply = async (): Promise<void> => {
        setApplying(true);
        setApplyError(null);
        try {
            await onApply();
            onClose();
        } catch (err) {
            setApplyError(err instanceof Error ? err.message : String(err));
        } finally {
            setApplying(false);
        }
    };

    return (
        <Dialog
            open
            onClose={onClose}
            size="l"
            contentOverflow="auto"
        >
            <Dialog.Header caption={`Review changes — ${configName}`} />
            <Dialog.Body>
                <Flex direction="column" gap={3}>
                    {applyError && (
                        <Text variant="caption-1" color="danger">{applyError}</Text>
                    )}
                    {warning}
                    <div style={{ maxHeight: '60vh', overflowY: 'auto' }}>
                        <SideBySideDiff changes={changes} />
                    </div>
                </Flex>
            </Dialog.Body>
            <Dialog.Footer
                onClickButtonCancel={onClose}
                onClickButtonApply={handleApply}
                textButtonCancel="Cancel"
                textButtonApply={applying ? `${applyLabel}…` : applyLabel}
                loading={applying}
                propsButtonApply={{ disabled: applying }}
            />
        </Dialog>
    );
};
