# git atlas

A place for keeping track of Dagger's git related APIs.

`index.html` is a single self-contained page: one entry per public git-related
field of the Dagger API, with one tab per pull request that changed how the
field is implemented (pseudocode, diff against the previous tab, code pointers
linked to the exact commit), plus a timeline, a consistency matrix of
mechanisms × fields, consolidation findings and a glossary. A dropdown at the
top right highlights any set of PRs everywhere (tabs, matrix cells, timeline
rows, findings); the selection is kept in the URL.

Published copy: <https://gistpreview.github.io/?f33895836cd94dfcc7caab754162019c>
(gist f33895836cd94dfcc7caab754162019c; see [Publishing](#publishing)).

Everything in the page comes from small hand-written files under `data/` plus
data derived from [dagger/dagger](https://github.com/dagger/dagger) by
`dagger generate`. Never edit `index.html`, `QUEUE.md` or `derived/` by hand.

## Layout

```
atlas.yaml                     what is tracked: upstream, cutoff, tracked paths, page text
data/
  prs/<n>.yaml                 one per PR with tabs: summary, why, effects, color
  prs/base.yaml                the baseline pseudo-PR (everything before the cutoff)
  apis/<Type.field>.yaml       one per public field: family, since, notes, aliases
  families/<id>.yaml           an implementation shared by several fields
  revisions/<family>/<pr>.yaml one tab: what/why, pointers, pseudocode, grounding
  revisions/<family>/<pr>.svg  optional diagram for that tab
  matrix/<Type.field>.yaml     per mechanism: status, note, PRs, evidence, as-of commit
  findings/<id>.yaml           consolidation findings
  mechanisms.yaml              the matrix columns
  glossary.yaml
  skipped.yaml                 PRs that touched tracked paths without changing a field
derived/upstream.json          GENERATED: upstream tip, PR metadata, signatures and docs
QUEUE.md                       GENERATED: coverage queue
index.html                     GENERATED: the page
site/template.html             the page's HTML/CSS/JS; data is injected at __DATA__
cmd/atlas/                     the Go tool behind generate and check
smoke/                         headless-browser smoke test
.dagger/main.dang              the Dagger module (generate + checks)
.agents/skills/process-pr/     how to add a new PR
```

### What is hand-written and what is derived

| | hand-written (`data/`) | derived (`derived/upstream.json`) |
|---|---|---|
| PR | summary, why, effects, color, `stack`/`merged_via` for stacked PRs, `title` only for the baseline or a stack | title, state (merged/open/closed), merge date and commit, head commit, base branch |
| API | family, `since` (`pre-cutoff` or a PR), notes, `also` (sibling fields on the same page), `aliases` (old deep links), `removed_in` | signature and doc, from `docs/docs-graphql/schema.graphqls` at the upstream tip (or at the head of the unmerged PR that introduces it) |
| tab | everything | blob URLs and line numbers of its pointers |

### Grounding

Each revision says how it was written:

- `source: code` — written from reading the code at the commit the tab
  describes; `source: pr-description` — mainly from the PR description and
  commit messages. When in doubt, say `pr-description`.
- `verified_at: null` until someone re-reads the code against the tab and
  confirms it; then the date (`YYYY-MM-DD`). Nothing is verified yet.
- `sha:` pins the commit a tab describes. Merged PRs default to their merge
  commit; unmerged PRs must pin their head, since heads move.

Pointers are `path[:symbol][@sha]`, or `{at: ..., note: ...}` to add a note.
`symbol` is `func`, `Type.method`, or a type/var/const name; `path` may also be
a package directory. In revisions a missing `@sha` means the tab's commit; in
the matrix (`evidence`) the file's `as_of`; findings must always spell it out.
Short SHAs are fine.

## Generate

```sh
dagger generate
```

runs the `git-atlas` module's generator, which:

1. fetches `main` of dagger/dagger (a blobless, shallow clone kept in a cache
   volume) and pins its tip in `derived/upstream.json`;
2. derives PR metadata from the merge commits on main's first-parent history
   (`Merge pull request #N …`, or `title (#N)` for squash merges), and for PRs
   that are not on main (open, closed, or merged into a stack branch) asks the
   GitHub API;
3. derives signatures and docs from the GraphQL schema;
4. assigns a color to every PR file without one (first free palette entry, in
   PR order; existing colors never move);
5. rewrites `QUEUE.md` and `index.html`.

The output is a pure function of the repository contents, the upstream tip
and GitHub's answers; running it twice in a row changes nothing.

**GitHub token.** `dagger.toml` passes `githubToken = "cmd://gh auth token"`
to the module. Use `env://GITHUB_TOKEN` instead if you prefer, or delete the
setting: without a token, generate still works. PRs on main are derived from
git alone; an unmerged PR keeps the title and state already recorded in
`derived/upstream.json` and takes its head from `git ls-remote
refs/pull/<n>/head`. A PR seen for the first time without a token gets state
`unknown`, which `validate` rejects, so add new unmerged PRs with a token.

Without Dagger, the same tool runs directly (needs Go and git):

```sh
go run ./cmd/atlas generate -refresh   # like dagger generate (reads GITHUB_TOKEN)
go run ./cmd/atlas generate            # re-render at the pinned upstream tip
go run ./cmd/atlas check validate      # or fresh, coverage
```

## Check

```sh
dagger check
```

| check | fails when |
|---|---|
| `git-atlas:validate` | a file breaks the schema (unknown or missing fields, bad enums, duplicate colors, malformed pointers); a revision's PR has no `data/prs` file; an API is not in the schema (and not marked `removed_in`); `since` disagrees with the schema at the cutoff; a reference to a PR, API, family or mechanism dangles; an SVG is not well-formed; **a code pointer does not resolve**: the file must exist at the commit and define the symbol |
| `git-atlas:fresh` | regenerating at the **pinned** upstream tip would change `index.html`, `QUEUE.md`, `derived/` or a PR color, i.e. someone edited data without running generate |
| `git-atlas:coverage` | `QUEUE.md` has blocking entries (below) |
| `git-atlas:unit` | the tool's unit tests fail |
| `git-atlas:smoke` | `index.html` fails in headless Chromium, loaded directly and the way gistpreview injects it (fetch + `document.write`): every tab and page must render, and the PR dropdown must select, persist to the URL, and highlight tabs, timeline rows, matrix cells and findings |

`fresh` regenerates at the pinned tip rather than the live one so that checks
are reproducible: they only start failing because of upstream activity after
someone runs `dagger generate`. For the same reason `dagger.toml` skips
Dagger's builtin `generate/stale` check, which re-runs the live generator and
would fail every time dagger/dagger merges anything.

**Coverage fails rather than warns.** A merged PR that touches a tracked path
and is neither described nor skipped means the atlas is silently wrong about
main, which is exactly what it exists to prevent. Clearing an entry is cheap
(a two-line `skipped.yaml` entry with a reason), and because the queue only
grows when someone runs `dagger generate`, a red `coverage` always means "you
pulled in new upstream history; triage it". Advisory entries never fail.

## Reading the queue

`QUEUE.md` has three sections:

- **Merged PRs touching tracked paths, not yet in the atlas** (blocking).
  Merged on main after the cutoff, touching a path in `atlas.yaml` `tracked`
  (minus `ignore`). Either describe it (`data/prs/<n>.yaml` plus a revision
  for each family whose implementation it changed) or add it to
  `data/skipped.yaml` with a reason. The listed files tell you where to look.
- **Tabs written against a pre-merge head** (blocking). A PR you described
  while it was open has merged. Re-read the tabs at the merge commit (rebases
  and review changes happen), fix what changed, and drop their `sha:` so they
  follow the merge commit.
- **Open PRs whose head moved** (advisory). Re-read when convenient and bump
  `sha:`.

Tracked paths are a judgment call: too wide and the queue fills with workspace
or module PRs that never touch git, too narrow and git changes slip through.
Edit `tracked`/`ignore` in `atlas.yaml` as the code moves.

To process a PR, follow [`.agents/skills/process-pr/SKILL.md`](.agents/skills/process-pr/SKILL.md).

## Publishing

The page is self-contained, so the gist is just a copy of `index.html`:

```sh
gh gist edit f33895836cd94dfcc7caab754162019c -f git-api-evolution.html index.html
```
