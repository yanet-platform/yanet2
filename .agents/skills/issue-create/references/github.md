# GitHub operation map

Use explicit repositories in every command. Never hardcode node IDs or reuse IDs
from agent memory: discover names and numbers immediately before a write.

## Preflight and discovery

- Verify `gh auth status`, repository visibility, labels, and write access.
- Search both repositories' issues and PRs before creating anything. Private
  search results never enter a public body.
- Query `repository.issueTypes` for `Bug | Feature | Task` and the target issue's
  `id`, type, labels, assignees, state, milestone, field values, project items,
  dependencies and sub-issues.
- Query `organization(login:"yanet-platform").issueFields` for live `Priority`
  and `Effort` field/option IDs.
- Query `organization.projectsV2` by project number. For each target project,
  discover its `Status` field and `Todo | In Progress | Done` option IDs. Read
  #11's current description before deciding membership.
- A missing or ambiguous expected name is a pre-write stop, not permission to
  choose the first result.

## Create and ordinary metadata

Feed body through stdin, never an argument or traced temporary file:

```bash
printf '%s' "$body" | gh issue create --repo "$repo" --title "$title" \
  --body-file - --label "$labels"
```

Omit `--label` when empty. Use `gh issue edit` for label changes and, when
content edits are authorized, `--body-file -`. Use `gh issue comment` for
provenance, missing-information and duplicate comments.

## GraphQL mutations

Query current state first, skip satisfied operations, and build mutations with
variables and the IDs just discovered:

- Type: `updateIssue(input:{id, issueTypeId})`.
- Priority/Effort: `setIssueFieldValue(input:{issueId, issueFields:[...]})`.
- Add board item: `addProjectV2ItemById(input:{projectId, contentId})`.
- Set Status: `updateProjectV2ItemFieldValue(input:{projectId,itemId,fieldId,value})`.
- Remove a wrong owned board: `deleteProjectV2Item(input:{projectId,itemId})`.
- Dependency: `addBlockedBy(input:{issueId,blockingIssueId})`.
- Epic child: `addSubIssue(input:{issueId,subIssueId})`.

After adding a project, query the issue again to obtain its new item ID before
setting Status. Container mode creates the fully drafted epic first, then creates
and immediately links each deduplicated child; a rerun reconciles the existing
partial tree.

## Reconciliation and read-back

If create returns a transport/5xx error, query recent non-PR issues in that
repository and filter by exact title, current viewer, and creation time. One
match is the created issue; no match permits one retry; multiple matches stop.

Success requires read-back of title/body, Type, exact managed labels,
Priority/Effort, one primary project and its Status, optional #11, dependencies,
and no assignee for a new issue. Report the issue URL plus every missing
postcondition; repair partial state with `triage`, never a second create.
