import React, { useMemo } from 'react';
import { Text } from '@gravity-ui/uikit';
import type { FIBRowItem } from './types';
import { rowsToDiffYaml } from './yaml';
import { countInvalidRows } from './validation';
import { SaveDiffModal } from '../../../components';

interface FIBSaveDiffModalProps {
    configName: string;
    draftRows: FIBRowItem[];
    serverRows: FIBRowItem[];
    onClose: () => void;
    onApply: () => Promise<void>;
}

/**
 * Modal showing a side-by-side YAML diff of server vs draft FIB rows for a config,
 * with a Commit button that calls onApply (which calls API.route.updateFIB) and closes on success.
 */
export const FIBSaveDiffModal: React.FC<FIBSaveDiffModalProps> = ({
    configName,
    draftRows,
    serverRows,
    onClose,
    onApply,
}) => {
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
