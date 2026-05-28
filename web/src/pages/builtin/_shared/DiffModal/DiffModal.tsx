import React, { useMemo, useState } from 'react';
import { Dialog, Flex, Text } from '@gravity-ui/uikit';
import { diffLines } from 'diff';
import { SideBySideDiff } from '../../../../components';

export interface DiffModalProps<T> {
    /** The locally-edited entity. */
    entity: T;
    /** The entity as last seen from the server. Pass null when creating a new entity. */
    serverEntity: T | null;
    /** Serialise an entity to a YAML string for diffing. */
    toYaml: (e: T) => string;
    /** Dialog header caption, e.g. "Review changes — my-function". */
    title: string;
    /** Called when the user confirms the apply action. May be async. */
    onApply: () => void | Promise<void>;
    /** Called when the dialog is dismissed. */
    onClose: () => void;
    /** Optional pre-flight error displayed in the error bar (prevents apply when set). */
    headerError?: string;
}

/**
 * Generic modal showing a side-by-side YAML diff of server vs local entity state.
 * Renders via Gravity UI Dialog and uses only global Gravity tokens for colors.
 */
export const DiffModal = <T,>({
    entity,
    serverEntity,
    toYaml,
    title,
    onApply,
    onClose,
    headerError,
}: DiffModalProps<T>): React.JSX.Element => {
    const [applying, setApplying] = useState(false);
    const [applyError, setApplyError] = useState<string | null>(null);

    const oldYaml = useMemo(() => serverEntity != null ? toYaml(serverEntity) : '', [serverEntity, toYaml]);
    const newYaml = useMemo(() => toYaml(entity), [entity, toYaml]);
    const changes = useMemo(() => diffLines(oldYaml, newYaml), [oldYaml, newYaml]);

    const errorMsg = headerError ?? applyError ?? null;
    const disableApply = applying || headerError != null;

    const handleApply = async (): Promise<void> => {
        if (headerError != null) {
            return;
        }
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
            <Dialog.Header caption={title} />
            <Dialog.Body>
                <Flex direction="column" gap={3}>
                    {errorMsg != null && (
                        <Text variant="caption-1" color="danger">{errorMsg}</Text>
                    )}
                    <SideBySideDiff changes={changes} />
                </Flex>
            </Dialog.Body>
            <Dialog.Footer
                onClickButtonCancel={onClose}
                onClickButtonApply={handleApply}
                textButtonCancel="Cancel"
                textButtonApply={applying ? 'Applying…' : 'Apply'}
                loading={applying}
                propsButtonApply={{ disabled: disableApply }}
            />
        </Dialog>
    );
};
