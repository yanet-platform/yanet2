import React, { useMemo, useState } from 'react';
import { Dialog, Flex, Text } from '@gravity-ui/uikit';
import * as yaml from 'js-yaml';
import { diffLines } from 'diff';
import type { Change } from 'diff';
import type { NetworkFunction } from '../types';

interface DiffModalProps {
    fn: NetworkFunction;
    serverFn: NetworkFunction | null;
    saveErrors: string[];
    onClose: () => void;
    onApply: () => Promise<void>;
}

/** Strip volatile / non-config fields before serialising for diff. */
const toComparableObj = (fn: NetworkFunction): object => ({
    id: fn.id,
    type: fn.type,
    ...(fn.description ? { description: fn.description } : {}),
    chains: fn.chains.map(c => ({
        id: c.id,
        name: c.name,
        weight: c.weight,
        modules: c.modules.map(m => ({
            id: m.id,
            type: m.type,
            name: m.name,
            ...(m.config && Object.keys(m.config).length > 0 ? { config: m.config } : {}),
        })),
    })),
});

const toYaml = (fn: NetworkFunction): string =>
    yaml.dump(toComparableObj(fn), { sortKeys: false, lineWidth: 120, noRefs: true });

/** One row in the side-by-side diff: left content (removed/context), right content (added/context). */
interface SbsRow {
    left: string | null;
    right: string | null;
    kind: 'added' | 'removed' | 'context';
    rowIdx: number;
}

/** Build side-by-side rows from a list of unified diff changes. */
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
    fontFamily: 'var(--fng-font-mono, ui-monospace, monospace)',
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
            fontFamily: 'var(--fng-font-mono, ui-monospace, monospace)',
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
 * Modal showing a side-by-side YAML diff of server vs local edits,
 * rendered via Gravity-UI Dialog.
 */
export const DiffModal: React.FC<DiffModalProps> = ({
    fn,
    serverFn,
    saveErrors,
    onClose,
    onApply,
}) => {
    const [applying, setApplying] = useState(false);
    const [applyError, setApplyError] = useState<string | null>(null);

    const oldYaml = useMemo(() => serverFn ? toYaml(serverFn) : '', [serverFn]);
    const newYaml = useMemo(() => toYaml(fn), [fn]);
    const changes = useMemo(() => diffLines(oldYaml, newYaml), [oldYaml, newYaml]);

    const hasWeightErrors = saveErrors.length > 0;
    const errorMsg = hasWeightErrors ? saveErrors[0] : applyError;

    const handleApply = async (): Promise<void> => {
        if (hasWeightErrors) {
            return;
        }
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
            className="fng-diff-dialog"
            contentOverflow="auto"
        >
            <Dialog.Header caption={`Review changes — ${fn.id}`} />
            <Dialog.Body className="fng-diff-dialog__body">
                <Flex direction="column" className="fng-diff-dialog__content">
                    {errorMsg && (
                        <div className="fng-diff-dialog__error-bar">
                            <Text variant="caption-1" color="danger">{errorMsg}</Text>
                        </div>
                    )}
                    <div className="fng-diff-dialog__scroll">
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
                propsButtonApply={{ disabled: applying || hasWeightErrors }}
            />
        </Dialog>
    );
};
