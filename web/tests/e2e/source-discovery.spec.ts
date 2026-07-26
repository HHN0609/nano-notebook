import { expect, test } from "@playwright/test";

test("keeps ready Research compact until View opens every source", async ({ page }, testInfo) => {
  const candidates = Array.from({ length: 10 }, (_, index) => ({
    id: `candidate_${index + 1}`,
    ordinal: index,
    title: `UCSD Research source ${index + 1}`,
    canonical_url: `https://example.com/research-${index + 1}`,
    display_url: `example.com/research-${index + 1}`,
    snippet: `A concise description of graduate research opportunity ${index + 1}.`,
    selected: true,
    status: "discovered"
  }));
  const savedSources = Array.from({ length: 5 }, (_, index) => ({
    id: `source_${index + 1}`,
    notebook_id: "nb_discovery",
    title: `Saved notebook source ${index + 1}`,
    format: "html",
    byte_size: 120,
    state: "ready",
    open_action: { kind: "external", href: `https://saved.example/source-${index + 1}` }
  }));

  await page.route("**/api/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    const responses: Record<string, unknown> = {
      "/api/v1/session": { user: { id: "usr_discovery", email: "research@example.com" } },
      "/api/v1/notebooks/nb_discovery": { notebook: { id: "nb_discovery", title: "Research notebook", role: "owner" } },
      "/api/v1/notebooks/nb_discovery/sources": { sources: savedSources },
      "/api/v1/notebooks/nb_discovery/chats": { chats: [{ id: "chat_discovery", notebook_id: "nb_discovery", title: "New chat" }] },
      "/api/v1/chats/chat_discovery": {
        chat: { id: "chat_discovery", notebook_id: "nb_discovery", title: "New chat" },
        messages: [], runs: [], citations: [], source_ids: savedSources.map((source) => source.id)
      },
      "/api/v1/chats/chat_discovery/source-selection": { source_ids: savedSources.map((source) => source.id) },
      "/api/v1/notebooks/nb_discovery/source-discovery-sessions/latest": {
        session: {
          id: "dss_ready",
          notebook_id: "nb_discovery",
          query: "ucsd graduate research opportunities",
          summary: "Official and practical resources for graduate research opportunities.",
          status: "ready",
          candidates
        }
      }
    };
    const payload = responses[path];
    await route.fulfill(payload ? { json: payload } : { status: 404, json: { error: { code: "not_found" } } });
  });

  await page.goto("/notebooks/nb_discovery");
  const compact = testInfo.project.name === "chromium-compact";
  const sources = compact
    ? page.locator(".workspace-compact-tabs").getByRole("tabpanel", { name: "Sources" })
    : page.locator(".workspace-panels").getByRole("region", { name: "Sources" });

  await expect(sources.getByText("Research completed")).toBeVisible();
  await expect(sources.locator(".source-discovery-preview")).toHaveCount(3);
  await expect(sources.getByRole("link", { name: /UCSD Research source 3/ })).toBeVisible();
  await expect(sources.getByRole("link", { name: /UCSD Research source 4/ })).toHaveCount(0);
  await expect(sources.getByText("Additional sources: 7")).toBeVisible();
  await expect(sources.getByText("Saved notebook source 1")).toBeVisible();
  await expect(page.locator(".workspace-panels")).not.toHaveClass(/workspace-panels--source-discovery/);
  const compactPanelWidth = await sources.evaluate((element) => element.getBoundingClientRect().width);
  await expectNoPageOverflow(page);
  await page.screenshot({ path: testInfo.outputPath("research-compact.png") });

  await sources.getByRole("button", { name: "View" }).click();
  await expect(sources.getByText("Source discovery")).toBeVisible();
  await expect(sources.getByRole("link", { name: /UCSD Research source 10/ })).toBeVisible();
  await expect(sources.getByRole("checkbox", { name: "UCSD Research source 10" })).toBeVisible();
  await expect(sources.getByText("Saved notebook source 1")).toHaveCount(0);
  const detailMetrics = await sources.evaluate((element) => {
    const title = element.querySelector<HTMLElement>(".source-discovery-result-copy a");
    const supporting = element.querySelector<HTMLElement>(".source-discovery-result-copy p");
    return {
      width: element.getBoundingClientRect().width,
      titleFontSize: title ? getComputedStyle(title).fontSize : "",
      supportingFontSize: supporting ? getComputedStyle(supporting).fontSize : ""
    };
  });
  if (compact) {
    expect(detailMetrics.titleFontSize).toBe("14px");
    expect(detailMetrics.supportingFontSize).toBe("12px");
  } else {
    await expect(page.locator(".workspace-panels")).toHaveClass(/workspace-panels--source-discovery/);
    expect(detailMetrics.width).toBeGreaterThan(compactPanelWidth);
    expect(detailMetrics.width).toBeGreaterThanOrEqual(640);
    expect(detailMetrics.width).toBeLessThanOrEqual(720);
    expect(detailMetrics.titleFontSize).toBe("15px");
    expect(detailMetrics.supportingFontSize).toBe("13px");
  }
  await expectNoPageOverflow(page);
  await page.screenshot({ path: testInfo.outputPath("research-detail.png") });

  if (!compact) {
    await page.setViewportSize({ width: 1100, height: 800 });
    const intermediateDetailWidth = await sources.evaluate((element) => element.getBoundingClientRect().width);
    expect(intermediateDetailWidth).toBeGreaterThanOrEqual(560);
    await expectNoPageOverflow(page);
  }

  await sources.getByRole("button", { name: "Close" }).click();
  await expect(sources.getByText("Research completed")).toBeVisible();
  await expect(sources.getByRole("link", { name: /UCSD Research source 4/ })).toHaveCount(0);
});

async function expectNoPageOverflow(page: import("@playwright/test").Page) {
  await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true);
}
