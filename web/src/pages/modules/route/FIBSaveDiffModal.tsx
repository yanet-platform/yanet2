import React from 'react';
import type { FIBRowItem } from './types';
import { rowsToDiffYaml } from './yaml';
import { countInvalidRows } from './validation';
import { DraftSaveDiffModal } from '@yanet/core/components';

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
export const FIBSaveDiffModal: React.FC<FIBSaveDiffModalProps> = (props) => (
    <DraftSaveDiffModal<FIBRowItem>
        {...props}
        rowsToDiffYaml={rowsToDiffYaml}
        countInvalidRows={countInvalidRows}
    />
);
