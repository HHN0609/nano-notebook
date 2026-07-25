import { expect, test } from "@playwright/test";

test("assistant Markdown stays readable without overflowing the workspace", async ({ page }, testInfo) => {
  await page.route("**/api/**", async (route) => {
    const url = new URL(route.request().url());
    const responses: Record<string, unknown> = {
      "/api/v1/session": { user: { id: "usr_markdown", email: "markdown@example.com" } },
      "/api/v1/notebooks/nb_test": { notebook: { id: "nb_test", title: "Markdown Notes" } },
      "/api/v1/notebooks/nb_test/sources": { sources: [] },
      "/api/v1/notebooks/nb_test/chats": { chats: [{ id: "chat_test", notebook_id: "nb_test", title: "New chat" }] },
      "/api/v1/chats/chat_test/source-selection": { source_ids: [] },
      "/api/v1/chats/chat_test": {
        chat: { id: "chat_test", notebook_id: "nb_test", title: "New chat" },
        messages: [{
          id: "msg_markdown",
          role: "assistant",
          content: [
            "## Study plan",
            "",
            "Use **retrieval** with `cached context`.",
            "",
            "- Read the notes",
            "- [x] Keep citations",
            "",
            "| Topic | Status |",
            "| --- | --- |",
            "| Cache | Ready |",
            "",
            "> Verify the evidence.",
            "",
            "```ts",
            "const cached = true;",
            "```"
          ].join("\n"),
          created_at: "2026-07-25T12:00:00Z"
        }],
        runs: [],
        citations: []
      }
    };
    const payload = responses[url.pathname];
    await route.fulfill(payload ? { json: payload } : { status: 404, json: { error: { code: "not_found" } } });
  });

  await page.goto("/notebooks/nb_test");
  const compact = testInfo.project.name === "chromium-compact";
  if (compact) {
    await page.getByRole("tab", { name: "Chat" }).click();
  }

  const chat = compact ? page.getByRole("tabpanel", { name: "Chat" }) : page.getByRole("region", { name: "Chat" });
  await expect(chat.getByRole("heading", { name: "Study plan", level: 2 })).toBeVisible();
  await expect(chat.getByRole("table")).toBeVisible();
  await expect(chat.getByText("const cached = true;")).toBeVisible();
  await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true);
  await page.screenshot({ path: testInfo.outputPath("chat-markdown.png"), fullPage: true });
});
