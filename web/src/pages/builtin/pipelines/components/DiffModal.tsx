import React from 'react';
import type { Pipeline } from '../types';
import { localToApi } from '../wire';
import { EntityDiffModal } from '@yanet/core/components';
import { dumpYamlDoc } from '@yanet/core/utils';

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
    <EntityDiffModal
        configName={pipeline.id}
        current={pipeline}
        server={serverPipeline}
        toYaml={toYaml}
        onClose={onClose}
        onApply={onApply}
    />
);
