# FoxOS repository instructions

## Scope and sources of truth

- These instructions apply to the entire FoxOS repository.
- FoxOS is a Go backend with SQLite, a React/TypeScript/Vite frontend, RouterOS lifecycle scripts, and Mihomo/MosDNS container assets.
- Read `README.md` for supported behavior and developer commands. Read `docs/release-and-routeros.md` and `deploy/routeros/QUICK-INSTALL.md` before release, CHR, installation, upgrade, rollback, or RouterOS work.
- Treat task handoffs, old test output, commit SHAs, CI links, and status snapshots as historical evidence until reverified. Do not store volatile release status in this file.

## Start every task

- Run `git status --short --branch` and `git log -5 --oneline --decorate` before editing.
- Confirm the current branch, HEAD, upstream, and dirty files instead of relying on a previous report.
- Check whether another Codex task is active in this working tree. Only one task may write at a time; read-only reviews may run concurrently when their boundary is explicit.
- Classify the request before acting: answer, diagnosis, read-only review, implementation, release, or device deployment. Do not turn one class into another without matching user authorization.
- For repeated repair rounds, architecture confusion, deployment or release churn, scope drift, or broad convergence work, use the installed `converge-engineering-work` skill before planning code changes.
- Inspect the affected code, nearby tests, and relevant documentation before choosing an implementation.
- Preserve all existing changes. Do not use destructive Git commands, switch branches, merge `main`, or rewrite history.
- Before any commit or push, fetch the remote, verify the upstream has not moved unexpectedly, recheck the complete diff, and scan for sensitive material. Commit, push, tag, and release remain disabled unless the user explicitly authorizes them.

## Work control and anti-loop protocol

- Keep exactly one current objective with a concrete definition of done. For substantial work, maintain one compact ledger with stable IDs such as `ARCH-001`, `DEP-001`, `CORE-001`, `WEB-001`, or `QA-001`; do not use `R1`, `R2`, or review-round numbers as defect identity.
- Move work forward through `DISCOVERED -> PLANNED -> IMPLEMENTING -> TARGETED_VERIFIED -> FULLY_VERIFIED -> DECIDED`. Return to an earlier state only when the frozen input changed or new reproducible evidence invalidates a closed item.
- Close a finding only after its implementation and required evidence pass. Do not reopen, re-audit, or restate an unchanged closed finding without new evidence.
- Every revision must map to a stable finding and a failing reproduction, regression test, or external acceptance result. Do not repeat the same failed approach more than twice; after two failures, form a different root-cause hypothesis, change the design, or report an exact blocker.
- Freeze scope before implementation. During an RC or deployment candidate, admit new work only for security, data loss, a broken core workflow, failed rollback, or a failed release gate. Put other findings in the backlog instead of extending the active candidate.
- Run targeted verification after each relevant change. Run the broad gate once for a frozen candidate, and rerun it only when affected input changes; unchanged green evidence must not trigger another full review cycle.
- A task must end in one of three states: passed with current evidence, blocked with a reproducible condition, or pending explicit authorization. Do not use "one more review" or a new revision label as progress.
- Report only the delta from the previous checkpoint: changed facts, newly closed IDs, current blocker, and next action.

## Implementation rules

- Prefer existing packages, APIs, scripts, and UI patterns. Keep changes scoped to the requested behavior.
- Network and state correctness take priority over presentation. External state uncertainty must fail closed and retain evidence.
- Keep configuration structured; do not parse JSON, YAML, URLs, or RouterOS state with fragile string manipulation when a parser or existing helper is available.
- Add tests for changed behavior and regressions. Do not weaken assertions, skip checks, or lower coverage thresholds to make a gate pass.
- Keep secrets, tokens, passwords, private keys, subscription URLs, RouterOS exports, environment values, and infrastructure details out of source, logs, screenshots, fixtures, and responses.
- Go production assets require a supported Go toolchain; `go.mod` intentionally retains Go 1.24 language semantics. The frontend expects Node.js 22.x.
- If `node` or `npm` is absent from `PATH`, use the bundled Codex Node runtime under `/Users/zl/.cache/codex-runtimes/codex-primary-runtime/dependencies/node/bin` and prepend that directory to `PATH` for the complete frontend or Playwright process.

## Architecture convergence rules

- Keep one machine-readable owner for deployment topology, paths, environment keys, version compatibility, resource ownership, and lifecycle states. Generate or validate downstream scripts, manifests, documentation, and tests from that contract instead of adding another hand-maintained copy.
- Put multi-step deployment orchestration, bounded retries, durable progress, and recovery decisions in a testable workstation-side Go component. Keep the Go service responsible for application data, quiescence, checkpoints, and structured status APIs. Keep RouterOS `.rsc` files limited to small idempotent discover, plan, apply, and verify primitives.
- Do not add a new long-running cross-system workflow to RouterOS scripts until the responsibility boundary and transition contract are documented in `docs/architecture.md` and covered by failure-injection tests.
- Define each cross-system lifecycle as one explicit transition table with operation identity, allowed source state, action, readback, retry limit, terminal state, and recovery action. Do not spread new ad hoc status strings or retry rules across Go, RouterOS, shell, and prose.
- Preserve one user-facing site configuration, one formal bundle, and one formal deployment entry. Test fixtures and compatibility helpers must be clearly internal and must not create a parallel release path.
- Documentation explains operator intent and recovery. Executable contracts and tests enforce machine behavior. Do not fix an ownership or state-model defect only by adding more regex assertions to a monolithic static checker.
- Simplify by moving complexity behind stable interfaces, not by removing backup, confirmation, ownership, readback, rollback, or fail-closed guarantees.

## Verification by change type

- Run the narrowest relevant test first, then expand verification according to blast radius. Report commands actually run and their results; list skipped gates separately.
- After changing Go files, format the exact changed files with `gofmt`, run relevant package tests, then normally run:

  ```bash
  go vet ./...
  go test -shuffle=on -count=1 ./...
  ```

- Run `go test -race -shuffle=on -count=1 ./...` for shared state, concurrency, persistence, recovery, release candidates, or broad backend changes. After dependency changes, also review `go mod tidy` output and run `go mod verify`.
- After frontend changes, run from `web/`:

  ```bash
  npm run typecheck
  npm run typecheck:e2e
  npm run test:unit
  npm run build
  ```

- Run `npm run test:e2e` for user-facing workflows, routing, responsive behavior, authentication, failure states, or shared UI state. Keep desktop, tablet, and 390px mobile coverage intact.
- After RouterOS or release-script changes, run from the repository root:

  ```bash
  bash scripts/check-routeros-scripts.sh
  bash scripts/check-sensitive-material.sh
  bash scripts/test-release-context.sh
  bash scripts/test-release-publisher.sh
  git diff --check
  ```

- For changed shell scripts, also run `bash -n` on the exact files. For workflow changes, run the pinned `actionlint` command used by `.github/workflows/core-ci.yml`.
- `scripts/test-network-namespace.sh`, Docker image builds, Trivy, SBOM generation, amd64 bundle construction, and full CI are environment-dependent gates. Run them when the host supports them; otherwise state precisely that they remain pending.
- Do not describe static checks, unit/integration tests, browser mocks, Linux namespaces, or image inspection as CHR or physical RouterOS acceptance.

## RouterOS and deployment safety

- RouterOS v7 only. Do not assume a target version, architecture label, container command property, bridge, storage path, or existing object state; read it from the exact target and verify compatibility.
- Use a disposable CHR matching the target RouterOS version for parser and `/container` add/get/delete behavior before a physical installation. CHR success is still not physical acceptance.
- Before any real RouterOS write, first complete read-only discovery and present the exact commands or scripts, affected objects, expected interruption, backup procedure, rollback procedure, verification steps, and stopping conditions. Wait for the user's explicit confirmation.
- Never use a production or physical router as the first syntax experiment. Never upload over colliding paths, reuse ambiguous objects, or delete unknown resources.
- Preserve RouterOS export and encrypted binary-backup evidence. Do not expose backup passwords or device credentials.
- Keep first install, upgrade, promote, rollback, cleanup, interruption recovery, CHR acceptance, and physical acceptance as separate results.
- Container `running`, an HTTP health response, Controller reachability, or a route marker does not prove transparent proxying, client traffic, DNS takeover, or rollback correctness.
- Treat a physical RouterOS as final acceptance, not as an iterative parser or packaging test environment. A device failure must first become a stable finding and an offline/CHR regression before another physical candidate is produced.
- Internal failed builds are development evidence, not `R` revisions. Assign a release-candidate label only after the frozen bundle passes all available offline gates and the exact-version CHR gate; if CHR is unavailable, stop with that gate explicitly blocked instead of creating another speculative candidate.

## Release rules

- A local green run is not a release. A release claim requires the exact candidate SHA, matching remote Core CI and CodeQL results, required image/security gates, and a downloadable bundle whose checksums and provenance were independently verified.
- Do not substitute artifacts from another commit, branch, workflow run, or release. Keep CI artifacts and formal Release assets distinct.
- Do not create or move tags, modify repository protection, bypass reviewers, publish a Release, or write `main` without explicit authorization.
- Never claim sing-box integration, transparent-proxy data-plane acceptance, CHR acceptance, or physical RouterOS deployment/rollback acceptance unless that exact layer has current evidence.

## Reporting

- Lead with the current conclusion and keep one compact checkpoint: current goal; completed and verified; in progress; blocked or unknown; next steps.
- Separate local uncommitted work, committed local work, pushed remote state, CI results, CHR results, and physical-device results.
- Include reproducible evidence for failures and reviews: severity, file and line, trigger, impact, minimal fix, and test gap.
- If a command was not run or an environment was unavailable, say so directly rather than implying success.
