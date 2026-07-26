# FoxOS Design QA

- Source visual truth:
  - `/root/.codex/generated_images/019f9d41-284e-72c1-9afe-7d3ae5a9d978/call_89FdAwbK1ar7tkcdlBkGvFXh.png`
  - `/root/.codex/generated_images/019f9d41-284e-72c1-9afe-7d3ae5a9d978/call_0HcGS2Yk8Zg2VrqWF0rsvM0s.png`
  - `/root/.codex/generated_images/019f9d41-284e-72c1-9afe-7d3ae5a9d978/call_b1MxBrAi7TnnKeWqHvtN6ZLa.png`
- Browser-rendered implementation evidence:
  - `docs/design/qa/overview-comparison.jpg`
  - `docs/design/qa/proxy-comparison.jpg`
  - `docs/design/qa/device-comparison.jpg`
- Browser: ChatGPT Work Mode cloud Chrome
- Captured viewport: 1363 × 936 for overview; 1348 × 926 for proxy and device pages
- Source pixels: 1487 × 1058
- Implementation density: 1 CSS pixel per screenshot pixel
- Normalization: source was center-cropped and resized to the implementation viewport before side-by-side comparison
- State: dark desktop application, primary/default state with first device and first node selected

## Full-view comparison evidence

The implementation preserves the selected visual system: deep navy surfaces, fox-orange navigation and primary actions, green system health, blue traffic, purple proxy and DNS semantics, restrained separators, dense tables, and fixed right-hand detail panels. The overview composition, proxy chain builder, node table, device table, and action hierarchy match the selected concepts at the normalized viewport.

## Focused region comparison evidence

- Overview topology: service strip, RouterOS-centered path, LAN/DNS read-only summaries, device policy preview, and active chain summary were compared in `overview-comparison.jpg`.
- Proxy management: chain ordering, node filters, table density, selected-row state, health actions, L2TP representation, and right detail panel were compared in `proxy-comparison.jpg`.
- Device management: summary metrics, search/filter bar, row selection, static-IP state, egress policy, warning copy, and apply action were compared in `device-comparison.jpg`.

## Required fidelity surfaces

- Fonts and typography: passed. System Chinese sans-serif fallbacks render cleanly; body copy remains readable at 10–12px in the captured high-density desktop state, with clear 14–19px headings.
- Spacing and layout rhythm: passed. Sidebar, 12px panel gaps, table row heights, summary cards, and detail panels maintain consistent alignment and density.
- Colors and tokens: passed. Semantic colors map consistently to health, traffic, proxy/DNS, warnings, and destructive actions.
- Image quality and assets: passed. The FoxOS brand mark is a generated raster asset; operational icons come from the existing Lucide icon system. No emoji, handcrafted SVG, or placeholder imagery is used.
- Copy and content: passed. Navigation and operational copy match the requested FoxOS scope; DNS controls are absent and MosDNS is visibly read-only.

## Interaction verification

- Navigation tested: 总览 → 代理节点 → 设备管理.
- Proxy workflow tested: open 添加节点, enter node name/server, submit, and verify the new node appears in the table and chain-node selector.
- Device workflow tested: apply the selected device's egress policy and verify the success confirmation.
- Primary controls inspected: 全链路检测, 订阅导入, 批量节点检测, chain stage selection/removal, fixed-IP state, filters, settings, and sidebar collapse.
- Scroll behavior tested after route changes.
- Console checked: no application-origin warnings or errors. Observed errors were emitted only by the cloud-browser extension and were unrelated to FoxOS.

## Comparison history

1. Initial device-page verification found one P2 issue: route navigation preserved the previous page's vertical scroll position, causing the device summary row to open partially out of view.
2. Fix: `navigate()` now resets the document scroll position after changing routes.
3. Post-fix evidence: `device-comparison.jpg` opens at scroll position 0 with the summary strip, table, and device details correctly visible.

## Findings

No actionable P0, P1, or P2 issues remain.

## Follow-up polish

- P3: the custom FoxOS logo has slightly more dark padding than the visual target; it remains sharp and readable at sidebar size.
- P3: connect visible metrics to live backend observations after the new node/device APIs are implemented.

## Final result

final result: passed
