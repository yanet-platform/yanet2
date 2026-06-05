import React from 'react';
import * as yaml from 'js-yaml';
import type { Pipeline } from '../types';
import { localToApi } from '../wire';
import { SaveDiffModal } from '../../../../components';

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
    <SaveDiffModal
        configName={pipeline.id}
        beforeYaml={serverPipeline != null ? toYaml(serverPipeline) : ''}
        afterYaml={toYaml(pipeline)}
        onApply={onApply}
        onClose={onClose}
    />
);
