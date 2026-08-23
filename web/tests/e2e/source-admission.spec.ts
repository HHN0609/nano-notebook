import { expect, test } from "@playwright/test";

test("explains a review-required Source and submits the pinned decision", async ({ page }, testInfo) => {
  let reviewBody: Record<string, unknown> | undefined;
  await page.route("**/api/**", async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    if (url.pathname === "/api/v1/sources/src_review/admission-review" && request.method() === "POST") {
      reviewBody = request.postDataJSON() as Record<string, unknown>;
      await route.fulfill({ json: { review: {
        id: "sarv_22222222222222222222222222222222",
        report_id: "sar_11111111111111111111111111111111",
        decision: "approve",
        created_at: "2026-08-23T00:00:01Z"
      } } });
      return;
    }
    const responses: Record<string, unknown> = {
      "/api/v1/session": { user: { id: "usr_admission", email: "admission@example.com" } },
      "/api/v1/notebooks/nb_admission": { notebook: { id: "nb_admission", title: "Verified research", role: "owner" } },
      "/api/v1/notebooks/nb_admission/sources": { sources: [{
        id: "src_review", notebook_id: "nb_admission", title: "Unconfirmed report", format: "html", byte_size: 2048,
        state: "processing", open_action: { kind: "none" }, admission: {
          report_id: "sar_11111111111111111111111111111111", status: "review_required", score: 0.52,
          signal_coverage: 0.7, exact_identity_match: false, policy_id: "source-admission-v1",
          policy_sha256: "a".repeat(64), mode: "enforcement"
        }
      }] },
      "/api/v1/sources/src_review/admission": { admission: {
        source_id: "src_review", notebook_id: "nb_admission", revision_id: "evr_review", mode: "enforcement",
        report: {
          id: "sar_11111111111111111111111111111111", policy_id: "source-admission-v1", policy_sha256: "a".repeat(64),
          status: "review_required", score: 0.52, signal_coverage: 0.7, exact_identity_match: false,
          components: { provenance: 0.3, extraction: 0.95 },
          reasons: ["extraction_complete", "exact_identity_required", "score_below_threshold"]
        },
        input: { provider_id: "brave", provider_attempts: 1, searches: [{ query: "Unconfirmed report", results: [] }] },
        provider_id: "brave", provider_attempts: 1, created_at: "2026-08-23T00:00:00Z"
      } },
      "/api/v1/notebooks/nb_admission/chats": { chats: [] },
      "/api/v1/notebooks/nb_admission/source-discovery-sessions/latest": null,
      "/api/v1/notebooks/nb_admission/studio-outputs": { outputs: [] }
    };
    const payload = responses[url.pathname];
    if (payload === null) {
      await route.fulfill({ status: 204 });
      return;
    }
    await route.fulfill(payload ? { json: payload } : { status: 404, json: { error: { code: "not_found" } } });
  });

  await page.goto("/notebooks/nb_admission");
  const sources = testInfo.project.name === "chromium-compact"
    ? page.locator(".workspace-compact-tabs").getByRole("tabpanel", { name: "Sources" })
    : page.locator(".workspace-panels").getByRole("region", { name: "Sources" });
  await sources.getByRole("button", { name: "Needs review · 52%" }).click();

  const dialog = page.getByRole("dialog", { name: "Source verification" });
  await expect(dialog).toBeVisible();
  await expect(dialog.getByText("52%")).toBeVisible();
  await expect(dialog.getByText("70%")).toBeVisible();
  await expect(dialog.getByText("No exact source identity was found.")).toBeVisible();
  await page.screenshot({ path: testInfo.outputPath("source-admission-review.png") });
  await dialog.getByRole("button", { name: "Approve source" }).click();
  await expect.poll(() => reviewBody).toEqual({
    report_id: "sar_11111111111111111111111111111111", decision: "approve", note: ""
  });
  await expect(dialog.getByText("Approved")).toBeVisible();
  await expect(async () => {
    const overflows = await page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth);
    expect(overflows).toBe(false);
  }).toPass();
});
