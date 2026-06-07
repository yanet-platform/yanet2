import jsYaml from 'js-yaml';

/** Serialize a document object to YAML using the project-standard dump options. */
export const dumpYamlDoc = (doc: unknown): string =>
    jsYaml.dump(doc, { sortKeys: false, lineWidth: 120, noRefs: true });

/**
 * Parse a YAML string containing a named list into typed row items.
 *
 * Throws with a descriptive message on any parse or structural failure.
 * Each row receives an id of the form `import-${idx}-${Date.now()}`.
 */
export const parseYamlList = <T extends { id: string }>(
    text: string,
    key: string,
    mapRow: (raw: unknown, idx: number) => Omit<T, 'id'>,
): T[] => {
    let parsed: unknown;
    try {
        parsed = jsYaml.load(text);
    } catch (e) {
        throw new Error(`YAML parse error: ${(e as Error).message}`);
    }

    if (!parsed || typeof parsed !== 'object') {
        throw new Error(`Expected a YAML object with a "${key}" list.`);
    }

    const doc = parsed as Record<string, unknown>;
    const array = Array.isArray(doc[key]) ? (doc[key] as unknown[]) : null;

    if (!array) {
        throw new Error(`Expected a top-level "${key}" list.`);
    }

    return array.map((raw: unknown, idx: number) => ({
        id: `import-${idx}-${Date.now()}`,
        ...mapRow(raw, idx),
    })) as T[];
};
