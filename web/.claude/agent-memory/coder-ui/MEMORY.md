# coder-ui memory

## Rules

- Use `pl-` CSS class prefix for pipelines page, `fn-` for functions page. Why: consistent with the existing pattern in FunctionsPage.scss.
- `@xyflow/react` has been removed from package.json — `useGraphEditor.ts` was deleted (its only consumer, the old ReactFlow pipelines page, was already removed). Why: dependency was fully dead after old pipelines page deletion.
- The `useDragState` / `getDragPayload` pattern from `_shared/lane-editor` treats `fromFnId` as the pipeline/function owner id, `fromChainId` as the sub-container id, `fromModIdx` as the position. For pipeline lane (no chains), pass `pipelineId` as both `fromFnId` and `fromChainId`.
- FunctionId type is imported from `api/pipelines` not `api/common` when used in pipelines page. Why: `pipelines.ts` re-exports it and has all pipeline-related types together.

## Project context

- Pipelines page (Stage 2) was rewritten to mirror FunctionsPage structure: reducer + wire + usePipelinesData + lane track with DnD. The page lives at `/builtin/pipelines`. Why: design consistency requested in Stage 2 spec.
- `@xyflow/react` was removed entirely in the cleanup after the old pipelines page (PipelineGraph.tsx, CounterEdge.tsx) was deleted; `useGraphEditor.ts` has also been deleted.
- Pre-existing TS errors (10 total): 9 in `api/index.ts` (ambiguous re-exports) and 1 in `DevicesPage.tsx`. These are out of scope.
