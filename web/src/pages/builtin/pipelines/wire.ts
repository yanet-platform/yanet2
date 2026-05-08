import type { Pipeline, FunctionRef } from './types';
import type { Pipeline as APIPipeline, FunctionId } from '../../../api/pipelines';

const makeRefId = (pipelineName: string, idx: number, fnName: string): string =>
    `pl:${pipelineName}::ref:${idx}::${fnName}`;

/** Convert an API Pipeline into the local Pipeline model. */
export const apiToLocal = (pl: APIPipeline): Pipeline => {
    const name = pl.id?.name ?? '';
    const refs: FunctionRef[] = (pl.functions ?? []).map((f, idx) => ({
        id: makeRefId(name, idx, f.name ?? ''),
        name: f.name ?? '',
    }));
    return { id: name, functions: refs };
};

/** Convert the local Pipeline back into API shape for save. */
export const localToApi = (pl: Pipeline): APIPipeline => ({
    id: { name: pl.id },
    functions: pl.functions.map((r): FunctionId => ({ name: r.name })),
});
