import React from 'react';
import type { Pipeline } from '../types';
import { localToApi } from '../wire';
import { SaveDiffModal } from '../../../../components';
import { dumpYamlDoc } from '../../../../utils';

interface DiffModalProps {
    pipeline: Pipeline;
    serverPipeline: Pipeline | null;
    onClose: () => void;
    onApply: () => Promise<void>;
}

const toYaml = (pl: Pipeline): string =>
    dumpYamlDoc((() => {
        const { id, ...body } = localToApi(pl);
        return { name: id?.name ?? '', ...body };
    })());

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
