import React from 'react';
import type { NetworkFunction } from '../types';
import { localToApi } from '../wire';
import { SaveDiffModal } from '../../../../components';
import { dumpYamlDoc } from '../../../../utils';

interface DiffModalProps {
    fn: NetworkFunction;
    serverFn: NetworkFunction | null;
    saveErrors: string[];
    onClose: () => void;
    onApply: () => Promise<void>;
}

const toYaml = (fn: NetworkFunction): string =>
    dumpYamlDoc((() => {
        const { id, ...fnBody } = localToApi(fn);
        return { name: id?.name ?? '', ...fnBody };
    })());

/** Modal showing a side-by-side YAML diff of server vs local function edits. */
export const DiffModal: React.FC<DiffModalProps> = ({
    fn,
    serverFn,
    saveErrors,
    onClose,
    onApply,
}) => (
    <SaveDiffModal
        configName={fn.id}
        beforeYaml={serverFn != null ? toYaml(serverFn) : ''}
        afterYaml={toYaml(fn)}
        onApply={onApply}
        onClose={onClose}
        headerError={saveErrors.length > 0 ? saveErrors[0] : undefined}
    />
);
