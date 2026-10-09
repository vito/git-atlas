---
name: process-pr
description: Add a dagger/dagger pull request to the git atlas, or clear a QUEUE.md entry. Use when QUEUE.md lists a PR, when asked to add an open PR (e.g. "track #14600"), or when a tracked PR merged or moved.
---

# Process a PR into the git atlas

The atlas describes how each public git field of the Dagger API is
implemented, one tab per PR that changed it. Read `README.md` for the data
model; this is the procedure.

## Tools

With the git-atlas expertise composed (it is, in this workspace), you have:

- `trackedDiff(pr, paths?, stat?)`: what a dagger/dagger PR changed under the
  tracked paths: a merged PR's merge commit against its first parent, an open
  PR's head against its merge base. The header names the commit to read at
  (and the `sha:` to pin for open PRs). Start with `stat: true`.
- `findSymbol(name, commit, paths?)`: where `refJoin`, `LocalGitRef.Tree` or
  `GitCheckoutBase` is defined at a commit, printed as ready-made pointers.
- `committer_fileAt` / `committer_show` with
  `from: "https://github.com/dagger/dagger#<sha>"` to read whole files or
  other commits; `gh ... --repo dagger/dagger` for descriptions and comments.
- The `git-atlas/generate` generator and `git-atlas/*` checks (README).

Outside an agent, the same helpers are `go run ./cmd/atlas tracked-diff
[-stat] <pr>` and `go run ./cmd/atlas find-symbol <name> <commit>`.

## 0. Refresh and pick the work

1. `dagger generate` (needs a GitHub token for unmerged PRs, see README).
2. Read `QUEUE.md`. Blocking entries make `dagger check` fail; work them
   oldest first, since later tabs diff against earlier ones. Open PRs never
   appear there on their own: you are told which ones to track.

## 1. Decide: describe or skip

Read the PR: `gh pr view <n> --repo dagger/dagger`, `trackedDiff(<n>)`, and
its commits.

Skip it when no public git field's *implementation path* changes: renames,
logging, error text, tests only, module/address parsing, CLI-only changes,
incidental edits. Add to `data/skipped.yaml`:

```yaml
- pr: 14600
  reason: One sentence on why no public field's path changed.
```

Otherwise describe it (steps 2–5). When unsure, describe it.

## 2. The PR file

`data/prs/<n>.yaml`:

```yaml
summary: >-
  One or two sentences: what changed, for whom.
why: Optional. The problem it solved, if not obvious from the summary.
effects: |-
  Optional, measured numbers only, one line per family ("tree: 10m23s → 4m53s
  cold resume"). Quote the PR; say where a number comes from.
```

Leave out `color` (generate assigns one) and `title`/state/dates (derived).
For a stack merged as one commit, add `stack: [..]` and `merged_via: <n>`.

## 3. One revision per changed family

Find the families whose path the PR changes (`data/families/`, and the
`family:` of each `data/apis/*.yaml`). For each, write
`data/revisions/<family>/<n>.yaml`:

```yaml
source: code            # code if you read the code at the commit; else pr-description
sha: <head sha>         # only while the PR is unmerged; omit once merged
verified_at: null       # a date only after someone re-reads code vs. tab
what: >-
  What changed in this family's path and why, in prose a reviewer can check.
effect: Optional measured effect for this family.
pointers:
  - core/git_local.go:LocalGitRef.Tree     # path:symbol, symbol = func, Type.method, type/var/const
  - {at: core/git.go:refJoin, note: negotiation tips}
code: |
  Start from the previous tab's code (the newest tab of this family before
  this PR) and edit it, so "diff vs previous tab" shows only this PR's change.
```

Rules:

- Read the code at the commit the tab describes: the merge commit for merged
  PRs (`trackedDiff` prints it), the head for open ones. Read
  `<path>` at that sha; do not describe main or your memory of it.
- Every pointer must name a file that exists at that commit and a symbol
  defined in it; `dagger check` verifies this. Get pointers from
  `findSymbol` rather than guessing the file. Prefer the resolver plus the
  one or two functions where the behavior changed.
- Pseudocode compresses the path (call → materialization → git commands →
  snapshot). Keep the previous tab's lines verbatim where nothing changed.
- `source: pr-description` is fine and honest when you only read the
  description; never claim `code` you did not read.
- An optional diagram goes next to it as `<n>.svg` (inline SVG, no scripts,
  `width='100%'`, a `viewBox`).

## 4. New or removed fields

A field the PR adds needs `data/apis/<Type.field>.yaml`:

```yaml
family: <existing or new family id>
since: 14600
notes: Optional.
```

A new family needs `data/families/<id>.yaml` (`title`, `short`, `order`). A
removed field gets `removed_in: <n>`. Signatures and docs are derived; do not
copy them.

## 5. Matrix and findings

- Update `data/matrix/<Type.field>.yaml` cells whose status the PR changes:
  `status` (yes | partial | gap | na), `note`, `prs` (add this PR),
  `requires: <n>` while the PR is unmerged, `evidence` pointers with `@sha`,
  and `as_of` (cell- or file-level) set to the commit you judged.
- Findings: if the PR fixes one, set `status: fixed` and `fixed_in: <n>`. If
  reading it reveals a new duplication, lag or conflict, add
  `data/findings/fNN-<slug>.yaml` with `discovered_in: <n>` and pointers
  carrying `@sha`.

## 6. Merged PRs that already had tabs (queue: "pre-merge head")

Re-read each tab at the merge commit, update `what`/`code`/pointers for any
change since the head you described, drop `sha:`, remove `requires:` from
matrix cells that now hold on main, and keep `source` honest.

## 7. Finish

```sh
dagger generate   # colors, derived data, QUEUE.md, index.html
dagger check      # validate, fresh, coverage, unit, smoke
```

Fix every `validate` failure (usually a pointer whose symbol moved or a
wrong file). Commit the data and the regenerated files together, one PR per
commit, e.g. `atlas: add #14600 (incremental bundle import)`.
