import { expect, test } from "@playwright/test";

test("Sources and Chat citations share the declared opening behavior", async ({ page }, testInfo) => {
  await page.route("**/api/**", async (route) => {
    const url = new URL(route.request().url());
    const responses: Record<string, unknown> = {
      "/api/v1/session": { user: { id: "usr_sources", email: "sources@example.com" } },
      "/api/v1/notebooks/nb_test": { notebook: { id: "nb_test", title: "Source opening" } },
      "/api/v1/notebooks/nb_test/sources": { sources: [
        { id: "src_web", notebook_id: "nb_test", title: "Go documentation", format: "html", byte_size: 120, state: "ready", open_action: { kind: "external", href: "https://go.dev/doc/" } },
        { id: "src_image", notebook_id: "nb_test", title: "diagram.png", format: "png", byte_size: 68, state: "ready", open_action: { kind: "inline_original", href: "/api/v1/sources/src_image/original-asset", media_type: "image/png" } },
        { id: "src_docx", notebook_id: "nb_test", title: "brief.docx", format: "docx", byte_size: 200, state: "ready", open_action: { kind: "none" } }
      ] },
      "/api/v1/notebooks/nb_test/chats": { chats: [{ id: "chat_test", notebook_id: "nb_test", title: "New chat" }] },
      "/api/v1/chats/chat_test/source-selection": { source_ids: ["src_web", "src_image", "src_docx"] },
      "/api/v1/chats/chat_test": {
        chat: { id: "chat_test", notebook_id: "nb_test", title: "New chat" },
        messages: [{ id: "msg_answer", role: "assistant", content: "The diagram shows the flow. [source:src_image]", created_at: "2026-07-25T12:00:00Z" }],
        runs: [],
        citations: [{ id: "cit_image", message_id: "msg_answer", source_id: "src_image", source_title: "diagram.png", reference_kind: "source", reference_ordinal: 0 }]
      }
    };
    if (url.pathname === "/api/v1/sources/src_image/original-asset") {
      await route.fulfill({ contentType: "image/png", body: Buffer.from("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=", "base64") });
      return;
    }
    const payload = responses[url.pathname];
    await route.fulfill(payload ? { json: payload } : { status: 404, json: { error: { code: "not_found" } } });
  });

  await page.goto("/notebooks/nb_test");
  const compact = testInfo.project.name === "chromium-compact";
  const visibleSources = compact
    ? page.locator(".workspace-compact-tabs").getByRole("tabpanel", { name: "Sources" })
    : page.locator(".workspace-panels").getByRole("region", { name: "Sources" });

  const webSource = visibleSources.getByRole("link", { name: "Go documentation" });
  await expect(webSource).toHaveAttribute("href", "https://go.dev/doc/");
  await expect(webSource).toHaveAttribute("target", "_blank");
  await expect(visibleSources.getByRole("link", { name: "brief.docx", exact: true })).toHaveCount(0);
  await expect(visibleSources.getByRole("button", { name: "brief.docx", exact: true })).toHaveCount(0);

  if (compact) await page.getByRole("tab", { name: "Chat" }).click();
  const visibleChat = compact
    ? page.locator(".workspace-compact-tabs").getByRole("tabpanel", { name: "Chat" })
    : page.locator(".workspace-panels").getByRole("region", { name: "Chat" });
  await visibleChat.getByRole("button", { name: "Citation 1 for diagram.png" }).click();

  if (compact) await expect(page.getByRole("tab", { name: "Sources" })).toHaveAttribute("aria-selected", "true");
  const original = visibleSources.getByRole("region", { name: "Original source diagram.png" });
  await expect(original).toBeVisible();
  await expect(original.getByRole("img", { name: "diagram.png" })).toHaveAttribute("src", "/api/v1/sources/src_image/original-asset");
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await visibleSources.getByRole("button", { name: "Back to Sources" }).click();
  await expect(visibleSources.getByRole("link", { name: "Go documentation" })).toBeVisible();
});
