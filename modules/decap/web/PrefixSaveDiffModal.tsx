import React from 'react';
import type { PrefixRowItem } from './types';
import { rowsToDiffYaml } from './yaml';
import { countInvalidRows } from './validation';
import { DraftSaveDiffModal } from '@yanet/core/components';

interface PrefixSaveDiffModalProps {
    configName: string;
    draftRows: PrefixRowItem[];
    serverRows: PrefixRowItem[];
    onClose: () => void;
    onApply: () => Promise<void>;
}

/**
 * Modal showing a side-by-side YAML diff of server vs draft prefix rows,
 * with a Commit button that calls onApply and closes on success.
 */
export const PrefixSaveDiffModal: React.FC<PrefixSaveDiffModalProps> = (props) => (
    <DraftSaveDiffModal<PrefixRowItem>
        {...props}
        rowsToDiffYaml={rowsToDiffYaml}
        countInvalidRows={countInvalidRows}
    />
);
