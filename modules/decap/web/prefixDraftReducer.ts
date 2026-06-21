import { createDraftReducer } from '@yanet/core/components/draft';
import type { DraftState, DraftAction } from '@yanet/core/components/draft';
import type { PrefixRowItem } from './types';

export type PrefixDraftState = DraftState<PrefixRowItem>;
export type PrefixDraftAction = DraftAction<PrefixRowItem>;

const { reducer: prefixDraftReducer, initialState: initialPrefixDraftState } = createDraftReducer<PrefixRowItem>({
    getId: (r) => r.id,
    equals: (a, b) => a.prefix === b.prefix,
});

export { prefixDraftReducer, initialPrefixDraftState };
