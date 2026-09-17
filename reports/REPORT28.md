# Report: Stage 28

## Summary

Stage 28 adds continuous integration via GitHub Actions
(`.github/workflows/ci.yml`), per `REPORT22.md` item 10: automatically
running the same checks this project has always run by hand at the
end of every stage — `go build`/`go vet`/`gofmt`/`go test -race` and
`cargo fmt --check`/`cargo clippy`/`cargo build`/`cargo test` — on
every push and every pull request, instead of relying on me to
remember. A CI status badge was added to the top of `README.md`.

The rest of this report is `STAGE28.md`'s own "Details" section,
answered as precisely as I can from what I actually know about how
GitHub Actions works — flagged clearly wherever I can't verify
something about *your specific account/repository* from inside this
sandbox (no GitHub credentials or network access here), and what to go
check yourself.

## What the workflow does

Two independent jobs, `go` and `rust`, both defined in the one file
`.github/workflows/ci.yml`:

```yaml
on:
  push:
  pull_request:
```

This means: every single `git push` to any branch of this repository,
and every pull request opened against it, triggers a run. (There is no
retroactive check of the 27 stages of history already in the repo —
this only starts applying to activity from the point this workflow
file itself is pushed, onward.)

Each job:

- **`go`**: `go build ./...`, `go vet ./...`, a `gofmt -l .` check that
  fails the job if any file isn't already formatted, then
  `go test -race ./...` (the race detector, matching this project's own
  standing practice since Stage 17 — see `docs/background.md`).
- **`rust`**: `cargo fmt --check`, `cargo clippy --all-targets -- -D
  warnings`, `cargo build --release`, `cargo test --release`.

Both jobs run in parallel (nothing makes one wait for the other), each
in its own fresh environment (see below), and the overall workflow
only shows green if *both* pass.

**I verified every one of these exact commands passes against the
current code before writing this report** (`go build`/`vet`/`gofmt -l`/
`test -race` all clean; `cargo fmt --check`/`clippy`/`build --release`/
`test --release` all clean) — the closest confirmation I can get to a
real run without actually being able to trigger GitHub Actions from
this sandbox (I have no push access or GitHub credentials here; only
you pushing this stage's commit will trigger the very first real run).

## Where do the checks run?

On a **GitHub-hosted runner** — a fresh, temporary virtual machine that
GitHub itself provisions (on its own cloud infrastructure, currently
Microsoft Azure) for the sole purpose of running one job, then
destroys immediately afterward. This is not your machine, not this
Claude Code sandbox, and not any server you manage — it's compute
GitHub provides as part of the Actions product.

`runs-on: ubuntu-latest` requests GitHub's standard Linux image
(whichever Ubuntu LTS release GitHub currently designates as
"latest" — this label's underlying image is updated by GitHub itself
over time, not something this workflow pins). The standard-tier Linux
runner GitHub documents for this label has:

- 2-core x86_64 CPU
- 7 GB RAM
- 14 GB of SSD storage

Every job gets its **own** independent VM — the `go` and `rust` jobs
here never share memory, disk, or CPU with each other. Nothing
persists on the VM itself between runs; the only things that carry
over from one run to the next are (a) whatever's committed to the
repository, and (b) whatever was explicitly saved to GitHub's separate
Actions cache service (this workflow does that for Rust's compiled
dependencies — see the `actions/cache` step — but deliberately not for
Go, which has no external dependencies worth caching).

## What are the computational limits?

**Time**: I set `timeout-minutes: 20` on both jobs explicitly — a
self-imposed ceiling. Without it, GitHub's own default per-job limit
is 6 hours, which is far too generous a safety net on its own (a
future concurrency bug that deadlocks instead of failing fast could
silently occupy a runner, and burn through your included minutes, for
hours before GitHub's own limit finally killed it). 20 minutes is
generous relative to how fast these checks actually run today (a few
seconds for Go; well under a minute for Rust, even from a cold cache)
but tight enough to catch a genuine hang quickly.

**Minutes/cost — this is the one thing I cannot check for you.**
GitHub-hosted runner minutes are billed differently depending on
whether a repository is public or private, and I have no way to query
that from this sandbox (no `gh` CLI or GitHub network access here) —
please check your repository's own Settings page if you want to know
for certain which of these applies to `wfdoran/vibe_sat`:

- **If the repository is public**: GitHub-hosted Linux runner minutes
  are free and unlimited. This is a standing GitHub policy for public
  repositories, not something that needs configuring.
- **If the repository is private**: minutes are drawn from your
  account's monthly included allowance (2,000 minutes/month on the
  free plan, more on paid plans), with Linux runner minutes counted at
  a 1x multiplier against that allowance (Windows counts 2x, macOS
  10x — irrelevant here since this workflow only uses Linux runners).
  Given how fast these particular jobs run, you'd need many hundreds
  of pushes in a month before this became a real constraint either
  way. Past the included allowance: if you have a spending limit set
  above $0, you're billed per additional minute; if not (the default
  for a personal account with no payment method on file), Actions
  simply stops running new jobs until the next monthly cycle rather
  than charging you anything.

**Concurrency**: personal/free accounts can run a number of jobs
simultaneously (around 20, though GitHub can adjust this) — irrelevant
here, since this workflow only ever has two jobs running at once.

**Cache storage**: `actions/cache` (used for Rust's dependencies) has
its own separate per-repository storage limit (historically around
10 GB total across all cached workflow data); GitHub evicts older
caches automatically once that's exceeded. This doesn't draw from your
Actions minutes at all.

## What happens on a failure? Do you get an email, or do you have to check back manually?

Several things happen automatically, with different visibility:

1. **GitHub records a status against the exact commit** you pushed —
   visible as a red ✗ (or green ✓ once fixed) right next to that
   commit everywhere GitHub shows commits (the repository's commit
   history, the branch view, and inline on any pull request that
   includes it).
2. **The "Actions" tab of the repository** lists every workflow run,
   pass or fail, with full logs for every single step — this is where
   you'd go to see *why* a run failed, not just that it did.
3. **The README badge** I added
   (`https://github.com/wfdoran/vibe_sat/actions/workflows/ci.yml/badge.svg`)
   gives a persistent, glance-able green/red indicator on the repo's
   own front page, without clicking into anything.
4. **Email/notification**: by default, GitHub sends you an email (and
   a web notification, the bell icon on github.com) when a workflow
   run **you triggered** — which an ordinary `git push` to your own
   repository counts as — **fails**. It does not, by default, email
   you for a run that *succeeds*. This is controlled by the "Actions"
   section of <https://github.com/settings/notifications>, and I
   believe it's the default state for most GitHub accounts —
   **but I cannot confirm your account's actual current setting from
   here, since I have no access to your GitHub account.** Please check
   that settings page once, so you know for certain whether you'll get
   an email or need to check the Actions tab/badge manually. If it
   turns out to be off, the fix is a checkbox on that same page.

So, in the common case (default notification settings, which this
project's usual workflow of "push directly to `main`" fits exactly):
**you should get an email when a check fails, and nothing when it
passes** — you shouldn't need to remember to go check.

## Design decisions worth flagging

- **No branch restriction on `push`/`pull_request`.** Given this
  project's history so far is entirely direct pushes to `main` (no
  long-lived feature branches), restricting to `branches: [main]`
  would have made no practical difference yet, but leaving it
  unrestricted means the workflow is already correct if that ever
  changes.
- **Go gets no dependency cache; Rust does.** `go_src` has zero
  external dependencies (`PROMPT.md`'s own constraint) and so no
  `go.sum` for `actions/setup-go`'s built-in cache to key off of —
  caching would be a pure no-op there, so I turned it off explicitly
  rather than leave a harmless-but-noisy warning in every run's log.
  `rust_src` has four real dependencies (`clap`, `rand`,
  `crossbeam-deque`, `arc-swap`, plus their own transitive
  dependencies) worth not recompiling from scratch every single push,
  so I added an explicit cache there, keyed on `Cargo.lock`'s contents
  (any dependency version bump automatically invalidates it).
- **`go-version-file: go_src/go.mod`, not a hardcoded version number.**
  This reads the `go 1.26.5` directive straight out of the existing
  `go.mod` rather than duplicating that number into the workflow file,
  so a future `go.mod` bump (as `PROMPT.md` already anticipates —
  "If you would like some feature from a later version, please
  indicate this") doesn't also require remembering to update CI.
- **`cargo test --release`/`cargo build --release`, not debug
  builds.** Matches how this project has actually built and tested
  Rust throughout (every performance-sensitive report's own
  measurements used release builds); using the same profile in CI
  means a CI failure reflects the same binary behavior the project's
  own manual verification has always relied on.
- **Only `ubuntu-latest`, no OS matrix.** `PROMPT.md` itself says to
  "assume the platform is a generic linux x86_64" — matching that
  directly rather than adding Windows/macOS runners that would test a
  platform this project has never targeted (and, for a private repo,
  would draw down the minutes allowance faster, since non-Linux
  runners are billed at a higher multiplier).
- **No third-party GitHub Actions beyond `actions/checkout`,
  `actions/setup-go`, and `actions/cache`** (all published and
  maintained by GitHub itself) **plus one plain `rustup` shell
  command** for installing `rustfmt`/`clippy`, rather than reaching
  for a third-party Rust-toolchain-setup action. This was a deliberate
  choice given `STAGE28.md`'s "I would like to understand how this
  works" — every step in this workflow is either a first-party GitHub
  action or a command you could type yourself.

## Testing

No code changed in either language this stage. I ran every command
the workflow runs, by hand, against the current tree (see "What the
workflow does" above) to confirm the workflow wouldn't immediately
fail on its first real run; I could not trigger an actual GitHub
Actions run from this sandbox, since doing so requires pushing to the
real repository, which only you can do.

## Command line arguments

None. This stage adds no `vibe_sat` behavior in either language.

## Open questions / notes for you

- **Please check <https://github.com/wfdoran/vibe_sat/settings> once**
  to see whether the repository is public or private, and
  <https://github.com/settings/notifications>'s "Actions" section to
  confirm your own failure-email setting — both directly affect the
  answers above, and I have no way to check either from this sandbox.
- The very first real run of this workflow will happen when you push
  this stage's commit — I'd suggest watching the repository's
  "Actions" tab (or just your email) right after that push, as a
  one-time sanity check that everything is wired up the way this
  report describes, before trusting it silently going forward.
- I did not add a branch-protection rule requiring this workflow to
  pass before a merge (that's a separate repository setting, under
  Settings → Branches, and only matters once pull requests become part
  of this project's workflow — not the case yet, per the git history).
  Happy to add a note about that, or actually configure it if you tell
  me to, in a future stage.
- No language/toolchain version changes needed.
