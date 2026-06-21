import React from 'react';
import { SaveDiffModal } from './SaveDiffModal';

export interface EntityDiffModalProps<T> {
    configName: string;
    current: T;
    server: T | null;
    toYaml: (entity: T) => string;
    saveErrors?: string[];
    onClose: () => void;
    onApply: () => Promise<void>;
}

/** Side-by-side YAML diff modal for a single editable entity. */
export const EntityDiffModal = <T,>({
    configName,
    current,
    server,
    toYaml,
    saveErrors,
    onClose,
    onApply,
}: EntityDiffModalProps<T>): React.ReactElement => (
    <SaveDiffModal
        configName={configName}
        beforeYaml={server != null ? toYaml(server) : ''}
        afterYaml={toYaml(current)}
        onApply={onApply}
        onClose={onClose}
        headerError={saveErrors != null && saveErrors.length > 0 ? saveErrors[0] : undefined}
    />
);
