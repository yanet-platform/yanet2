import React, { useMemo } from 'react';
import { Text } from '@gravity-ui/uikit';
import { SaveDiffModal } from './SaveDiffModal';

export interface DraftSaveDiffModalProps<T> {
    configName: string;
    draftRows: T[];
    serverRows: T[];
    onClose: () => void;
    onApply: () => Promise<void>;
    rowsToDiffYaml: (rows: T[]) => string;
    countInvalidRows: (rows: T[]) => number;
}

/** Generic draft save-diff modal, parameterized by row type and YAML/validation helpers. */
export const DraftSaveDiffModal = <T,>({
    configName,
    draftRows,
    serverRows,
    onClose,
    onApply,
    rowsToDiffYaml,
    countInvalidRows,
}: DraftSaveDiffModalProps<T>): React.JSX.Element => {
    const beforeYaml = useMemo(() => rowsToDiffYaml(serverRows), [serverRows]);
    const afterYaml = useMemo(() => rowsToDiffYaml(draftRows), [draftRows]);
    const invalidCount = useMemo(() => countInvalidRows(draftRows), [draftRows]);

    const warning = invalidCount > 0 ? (
        <Text variant="caption-1" color="warning">
            {invalidCount} row{invalidCount === 1 ? '' : 's'} fail client-side validation — server may reject.
        </Text>
    ) : undefined;

    return (
        <SaveDiffModal
            configName={configName}
            beforeYaml={beforeYaml}
            afterYaml={afterYaml}
            warning={warning}
            applyLabel="Commit"
            onClose={onClose}
            onApply={onApply}
        />
    );
};
