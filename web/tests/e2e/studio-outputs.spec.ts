import { expect, test } from "@playwright/test";

test("Studio renders and reopens all four durable output types", async ({ page }, testInfo) => {
  await page.route("**/api/**", async (route) => {
    const url = new URL(route.request().url());
    const responses: Record<string, unknown> = {
      "/api/v1/session": { user: { id: "usr_studio", email: "studio@example.com" } },
      "/api/v1/notebooks/nb_test": { notebook: { id: "nb_test", title: "Studio Research", role: "owner" } },
      "/api/v1/notebooks/nb_test/sources": { sources: [
        { id: "src_one", notebook_id: "nb_test", title: "research.pdf", format: "pdf", byte_size: 2048, state: "ready", open_action: { kind: "none" } }
      ] },
      "/api/v1/notebooks/nb_test/chats": { chats: [{ id: "chat_test", notebook_id: "nb_test", title: "New chat" }] },
      "/api/v1/chats/chat_test/source-selection": { source_ids: ["src_one"] },
      "/api/v1/chats/chat_test": { chat: { id: "chat_test", notebook_id: "nb_test", title: "New chat" }, messages: [], runs: [], citations: [], source_ids: ["src_one"] },
      "/api/v1/notebooks/nb_test/studio-outputs": { outputs: studioOutputs() }
    };
    const payload = responses[url.pathname];
    await route.fulfill(payload ? { json: payload } : { status: 404, json: { error: { code: "not_found" } } });
  });

  await page.goto("/notebooks/nb_test");
  const compact = testInfo.project.name === "chromium-compact";
  if (compact) await page.getByRole("tab", { name: "Studio" }).click();
  const studio = compact
    ? page.locator(".workspace-compact-tabs").getByRole("tabpanel", { name: "Studio" })
    : page.locator(".workspace-panels").getByRole("region", { name: "Studio" });

  await expect(studio).toBeVisible();
  await expect(studio.locator(".studio-action-card")).toHaveCount(4);
  for (const label of ["Report", "Flashcards", "Mind map", "Data table"]) await expect(studio.getByRole("button", { name: label })).toBeVisible();
  await expect(studio.getByRole("button", { name: "Quiz" })).toHaveCount(0);
  await expect(studio.getByRole("heading", { name: "Recent" })).toBeVisible();
  await page.screenshot({ path: testInfo.outputPath("studio-recent.png"), fullPage: true });

  await studio.getByRole("button", { name: "Attention Research", exact: true }).click();
  const report = page.getByRole("dialog", { name: "Attention Research" });
  await expect(report.getByText("A compact account of the key findings.")).toBeVisible();
  await page.screenshot({ path: testInfo.outputPath("studio-report.png"), fullPage: true });
  await report.getByRole("button", { name: "Close" }).click();

  await studio.getByRole("button", { name: "Attention Cards", exact: true }).click();
  const cards = page.getByRole("dialog", { name: "Attention Cards" });
  await expect(cards.getByText("What is attention?")).toBeVisible();
  await cards.getByRole("button", { name: "Flip card" }).click();
  await expect(cards.getByText("A mechanism for weighting context.")).toBeVisible();
  await cards.getByRole("button", { name: "Close" }).click();

  await studio.getByRole("button", { name: "Attention Map", exact: true }).click();
  const mindMap = page.getByRole("dialog", { name: "Attention Map" });
  await expect(mindMap.getByText("Architecture")).toBeVisible();
  await mindMap.getByRole("button", { name: "Attention" }).click();
  await expect(mindMap.getByText("Architecture")).toBeHidden();
  await mindMap.getByRole("button", { name: "Close" }).click();

  await studio.getByRole("button", { name: "Attention Table", exact: true }).click();
  const table = page.getByRole("dialog", { name: "Attention Table" });
  await expect(table.getByRole("table")).toBeVisible();
  await expect(table.getByRole("columnheader", { name: "Concept" })).toBeVisible();
  await expect(table.getByRole("cell", { name: "Self-attention" })).toBeVisible();
  await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true);
});

function studioOutputs() {
  const common = { notebook_id: "nb_test", locale: "en", source_count: 1, status: "completed", created_at: "2026-07-28T08:00:00Z", updated_at: "2026-07-28T08:01:00Z" };
  return [
    { ...common, id: "out_report", run_id: "run_report", kind: "report", title: "Attention Research", artifact: { title: "Attention Research", summary: "A compact account of the key findings.", sections: [{ id: "sec_1", heading: "Key finding", markdown: "Attention is all you need.", source_ids: [] }] } },
    { ...common, id: "out_cards", run_id: "run_cards", kind: "flashcards", title: "Attention Cards", artifact: { title: "Attention Cards", cards: [
      { id: "card_1", front: "What is attention?", back: "A mechanism for weighting context.", source_ids: [] },
      { id: "card_2", front: "Card 2", back: "Answer 2", source_ids: [] }, { id: "card_3", front: "Card 3", back: "Answer 3", source_ids: [] },
      { id: "card_4", front: "Card 4", back: "Answer 4", source_ids: [] }, { id: "card_5", front: "Card 5", back: "Answer 5", source_ids: [] }
    ] } },
    { ...common, id: "out_map", run_id: "run_map", kind: "mind_map", title: "Attention Map", artifact: { title: "Attention Map", nodes: [
      { id: "root", parent_id: null, label: "Attention", detail: "Core topic", source_ids: [] },
      { id: "architecture", parent_id: "root", label: "Architecture", detail: "Transformer blocks", source_ids: [] },
      { id: "training", parent_id: "root", label: "Training", detail: "Optimization", source_ids: [] }
    ] } },
    { ...common, id: "out_table", run_id: "run_table", kind: "data_table", title: "Attention Table", artifact: { title: "Attention Table", description: "A structured comparison.", columns: ["Concept", "Role"], rows: [
      { id: "row_1", cells: ["Self-attention", "Weights context"], source_ids: [] }
    ] } }
  ];
}
