import { createDraftReducer } from '@yanet/core/components/draft';
import type { DraftState, DraftAction } from '@yanet/core/components/draft';
import type { FIBRowItem } from './types';

export type FIBDraftState = DraftState<FIBRowItem>;
export type FIBDraftAction = DraftAction<FIBRowItem>;

const { reducer: fibDraftReducer, initialState: initialFIBDraftState } = createDraftReducer<FIBRowItem>({
    getId: (r) => r.id,
    equals: (a, b) =>
        a.from === b.from &&
        a.to === b.to &&
        a.dst_mac === b.dst_mac &&
        a.src_mac === b.src_mac &&
        a.device === b.device &&
        a.counter === b.counter,
});

export { fibDraftReducer, initialFIBDraftState };
