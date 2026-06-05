import React from 'react';
import * as yaml from 'js-yaml';
import type { NetworkFunction } from '../types';
import { localToApi } from '../wire';
import { SaveDiffModal } from '../../../../components';

interface DiffModalProps {
    fn: NetworkFunction;
    serverFn: NetworkFunction | null;
    saveErrors: string[];
    onClose: () => void;
    onApply: () => Promise<void>;
}

const toYaml = (fn: NetworkFunction): string =>
    yaml.dump(
        (() => {
            const { id, ...fnBody } = localToApi(fn);
            return { name: id?.name ?? '', ...fnBody };
        })(),
        { sortKeys: false, lineWidth: 120, noRefs: true },
    );

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
