import React, { useMemo, useState } from 'react';
import { Dialog, Flex, Text } from '@gravity-ui/uikit';
import * as yaml from 'js-yaml';
import { diffLines } from 'diff';
import type { Change } from 'diff';
import type { Pipeline } from '../types';
import { localToApi } from '../wire';

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

interface SbsRow {
    left: string | null;
    right: string | null;
    kind: 'added' | 'removed' | 'context';
    rowIdx: number;
}

const buildSbsRows = (changes: Change[]): SbsRow[] => {
    const rows: SbsRow[] = [];
    let rowIdx = 0;

    for (const change of changes) {
        const lines = change.value.split('\n');
        const trimmed = lines[lines.length - 1] === '' ? lines.slice(0, -1) : lines;

        if (change.removed) {
            for (const line of trimmed) {
                rows.push({ left: line, right: null, kind: 'removed', rowIdx: rowIdx++ });
            }
        } else if (change.added) {
            for (const line of trimmed) {
                rows.push({ left: null, right: line, kind: 'added', rowIdx: rowIdx++ });
            }
        } else {
            for (const line of trimmed) {
                rows.push({ left: line, right: line, kind: 'context', rowIdx: rowIdx++ });
            }
        }
    }

    return rows;
};

const CELL_STYLE: React.CSSProperties = {
    flex: 1,
    minWidth: 0,
    overflowX: 'auto',
    fontFamily: 'var(--pl-font-mono, ui-monospace, monospace)',
    fontSize: 12,
    lineHeight: '1.6',
    whiteSpace: 'pre',
    padding: '0 12px',
    userSelect: 'text',
};

const ROW_STYLE: React.CSSProperties = {
    display: 'flex',
    minHeight: '1.6em',
    borderBottom: '1px solid transparent',
};

const getRowBg = (kind: SbsRow['kind'], side: 'left' | 'right'): string => {
    if (kind === 'removed' && side === 'left') {
        return 'color-mix(in srgb, var(--g-color-text-danger) 10%, transparent)';
    }
    if (kind === 'added' && side === 'right') {
        return 'color-mix(in srgb, var(--g-color-text-positive) 10%, transparent)';
    }
    return 'transparent';
};

const getTextColor = (kind: SbsRow['kind'], side: 'left' | 'right'): string => {
    if (kind === 'removed' && side === 'left') {
        return 'var(--g-color-text-danger)';
    }
    if (kind === 'added' && side === 'right') {
        return 'var(--g-color-text-positive)';
    }
    if (kind === 'context') {
        return 'var(--g-color-text-secondary)';
    }
    return 'var(--g-color-text-hint)';
};

const SideBySideDiff = ({ changes }: { changes: Change[] }): React.JSX.Element => {
    const rows = useMemo(() => buildSbsRows(changes), [changes]);

    return (
        <div style={{
            display: 'flex',
            fontFamily: 'var(--pl-font-mono, ui-monospace, monospace)',
            fontSize: 12,
            lineHeight: '1.6',
            border: '1px solid var(--g-color-line-generic)',
            borderRadius: 4,
            overflow: 'hidden',
        }}>
            <div style={{ flex: 1, minWidth: 0, borderRight: '1px solid var(--g-color-line-generic)' }}>
                <div style={{
                    padding: '2px 12px',
                    background: 'var(--g-color-base-generic)',
                    borderBottom: '1px solid var(--g-color-line-generic)',
                    fontSize: 11,
                    color: 'var(--g-color-text-hint)',
                    fontWeight: 600,
                    letterSpacing: '0.3px',
                    textTransform: 'uppercase',
                }}>
                    Before (server)
                </div>
                {rows.map(row => (
                    <div
                        key={`l-${row.rowIdx}`}
                        style={{
                            ...ROW_STYLE,
                            background: getRowBg(row.kind, 'left'),
                        }}
                    >
                        <span style={{
                            ...CELL_STYLE,
                            color: getTextColor(row.kind, 'left'),
                        }}>
                            {row.left ?? ''}
                        </span>
                    </div>
                ))}
            </div>
            <div style={{ flex: 1, minWidth: 0 }}>
                <div style={{
                    padding: '2px 12px',
                    background: 'var(--g-color-base-generic)',
                    borderBottom: '1px solid var(--g-color-line-generic)',
                    fontSize: 11,
                    color: 'var(--g-color-text-hint)',
                    fontWeight: 600,
                    letterSpacing: '0.3px',
                    textTransform: 'uppercase',
                }}>
                    After (local)
                </div>
                {rows.map(row => (
                    <div
                        key={`r-${row.rowIdx}`}
                        style={{
                            ...ROW_STYLE,
                            background: getRowBg(row.kind, 'right'),
                        }}
                    >
                        <span style={{
                            ...CELL_STYLE,
                            color: getTextColor(row.kind, 'right'),
                        }}>
                            {row.right ?? ''}
                        </span>
                    </div>
                ))}
            </div>
        </div>
    );
};

/**
 * Modal showing a side-by-side YAML diff of server vs local pipeline edits.
 */
export const DiffModal: React.FC<DiffModalProps> = ({
    pipeline,
    serverPipeline,
    onClose,
    onApply,
}) => {
    const [applying, setApplying] = useState(false);
    const [applyError, setApplyError] = useState<string | null>(null);

    const oldYaml = useMemo(() => serverPipeline ? toYaml(serverPipeline) : '', [serverPipeline]);
    const newYaml = useMemo(() => toYaml(pipeline), [pipeline]);
    const changes = useMemo(() => diffLines(oldYaml, newYaml), [oldYaml, newYaml]);

    const handleApply = async (): Promise<void> => {
        setApplying(true);
        setApplyError(null);
        try {
            await onApply();
            onClose();
        } catch (err) {
            setApplyError(err instanceof Error ? err.message : String(err));
        } finally {
            setApplying(false);
        }
    };

    return (
        <Dialog
            open
            onClose={onClose}
            size="l"
            className="pl-diff-dialog"
            contentOverflow="auto"
        >
            <Dialog.Header caption={`Review changes — ${pipeline.id}`} />
            <Dialog.Body className="pl-diff-dialog__body">
                <Flex direction="column" className="pl-diff-dialog__content">
                    {applyError && (
                        <div className="pl-diff-dialog__error-bar">
                            <Text variant="caption-1" color="danger">{applyError}</Text>
                        </div>
                    )}
                    <div className="pl-diff-dialog__scroll">
                        <SideBySideDiff changes={changes} />
                    </div>
                </Flex>
            </Dialog.Body>
            <Dialog.Footer
                onClickButtonCancel={onClose}
                onClickButtonApply={handleApply}
                textButtonCancel="Cancel"
                textButtonApply={applying ? 'Applying…' : 'Apply'}
                loading={applying}
                propsButtonApply={{ disabled: applying }}
            />
        </Dialog>
    );
};
