import React from 'react';
import * as yaml from 'js-yaml';
import type { Pipeline } from '../types';
import { localToApi } from '../wire';
import { DiffModal as SharedDiffModal } from '../../_shared/DiffModal';

interface DiffModalProps {
    pipeline: Pipeline;
    serverPipeline: Pipeline | null;
    onClose: () => void;
    onApply: () => Promise<void>;
}

const toYaml = (pl: Pipeline): string =>
    yaml.dump(
        (() => {
            const { id, ...body } = localToApi(pl);
            return { name: id?.name ?? '', ...body };
        })(),
        { sortKeys: false, lineWidth: 120, noRefs: true },
    );

/** Modal showing a side-by-side YAML diff of server vs local pipeline edits. */
export const DiffModal: React.FC<DiffModalProps> = ({
    pipeline,
    serverPipeline,
    onClose,
    onApply,
}) => (
    <SharedDiffModal
        entity={pipeline}
        serverEntity={serverPipeline}
        toYaml={toYaml}
        title={`Review changes — ${pipeline.id}`}
        onApply={onApply}
        onClose={onClose}
    />
);
