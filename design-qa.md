# FoxOS Design QA

This record describes browser acceptance of the current FoxOS implementation. It
does not claim acceptance against a physical RouterOS host.

## Evidence

- Desktop screenshot: `docs/screenshots/overview-desktop.jpg`
- Browser: Playwright against the local Vite build and an isolated mock FoxOS
  API, plus the Codex in-app Browser against the production `web/dist` served
  by a real local FoxOS Go process
- Viewports: 1440x900 desktop, 834x1112 tablet, and 390x844 mobile
- Automated browser gate: 55/55 Vitest assertions passed; the three-viewport
  Playwright suite passed 83 tests with 25 intentional capability skips, and
  five critical asynchronous workflows passed 25/25 across five repeats
- Browser console: no application warnings or errors; only Vite connection and
  React development-mode messages were present
- Real runtime smoke: session login, HttpOnly cookie restore after reload,
  settings/site readback, proxy-page independent failure states, logout, and a
  separate CSRF reject/accept matrix passed. RouterOS and Mihomo were left
  unconfigured, and no dangerous write was issued against a real dependency.

## Visual direction

The interface keeps a restrained, high-density network operations console:
dark neutral surfaces, compact status rows and tables, orange primary actions,
and distinct semantic colors for health, traffic, Mihomo, MosDNS, warnings, and
destructive actions. Headings, controls, data provenance, and timestamps remain
legible without turning operational sections into decorative cards.

## Responsive acceptance

- Desktop: the overview, proxy, device, and operations views fit at 1440x900
  without root horizontal overflow.
- Tablet: proxy, device, and operations views fit at 834x1112 without root
  horizontal overflow; dense sections stack into readable columns.
- Mobile: the operations and overview views fit at 390x844 without root
  horizontal overflow. The sidebar becomes an overlay navigation surface.
- Mobile navigation traps keyboard focus while open, closes with Escape, and
  restores focus to the navigation trigger.

## Credibility and failure states

- Each live data surface exposes its source, last update, loading, unavailable,
  and stale state.
- Enabling the RouterOS failure fixture left Mihomo, MosDNS, and SQLite-backed
  node data live. The overview derived its live-source count from the current
  fixture and retained previous RouterOS values as stale instead of substituting
  demo data; this document does not cache a fixed source count.
- A live non-service data source uses the normal status marker; unavailable and
  stale resources use their own markers. This regression is covered in Vitest.
- Mihomo publish confirmation lists the hash, changed-line count, affected
  runtime paths, one-time-token behavior, and rollback condition. The rollback
  fixture produced the explicit result: publish failed and the previous snapshot
  was restored.

## Accessibility and interaction acceptance

- Hash deep links, browser Back, and browser Forward update the selected view.
- A skip link, semantic landmarks, visible focus states, form labels, status
  announcements, and keyboard-operable tables are present.
- Destructive dialogs use dialog semantics, move and trap focus, close with
  Escape, restore focus to the invoking control, and require a separate impact
  acknowledgement before execution.
- Node deletion, Mihomo publish, snapshot restore, backup restore, and other
  state-changing operations describe their impact before confirmation.

## Scope boundary

Automated and browser acceptance use isolated substitutes for RouterOS and
Mihomo. The screenshot and results above prove the web behavior only. Physical
RouterOS container installation, management-plane bypass protection, real
container health, and rollback still require the read-only preflight, an exact
change plan, backups, an upload channel, and explicit operator confirmation.

## Result

Passed for automated desktop, tablet, mobile, keyboard, failure-isolation, and
rollback UI acceptance, plus local Go runtime authentication and readback.
CHR and physical RouterOS acceptance remain pending.
