import { expect, test } from "@playwright/test";

test("switches to Research, edits the durable plan, and starts execution", async ({ page }, testInfo) => {
  let messageID = "";
  let planVersion = 1;
  let status: "awaiting_confirmation" | "queued" = "awaiting_confirmation";
  let startedVersion = 0;
  let plan = {
    title: "Agent Harness architecture research",
    objective: "Choose a durable Agent Harness architecture",
    scope: "Open-source implementations",
    research_questions: ["How do their AgentLoops differ?"],
    investigation_tracks: ["Source code", "Independent evaluations"],
    source_strategy: ["Primary repositories", "Technical reports"],
    analysis_method: ["Compare on shared dimensions"],
    deliverable_outline: ["Executive summary", "Comparison", "Recommendation"],
    completion_criteria: ["Important claims are backed by read pages"],
    clarifying_questions: [] as string[]
  };

  await page.route("**/api/**", async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    const method = request.method();
    if (url.pathname === "/api/v1/session") return route.fulfill({ json: { user: { id: "usr_research", email: "research@example.com" } } });
    if (url.pathname === "/api/v1/notebooks/nb_research") return route.fulfill({ json: { notebook: { id: "nb_research", title: "Harness Research" } } });
    if (url.pathname === "/api/v1/notebooks/nb_research/sources") return route.fulfill({ json: { sources: [] } });
    if (url.pathname === "/api/v1/notebooks/nb_research/studio-outputs") return route.fulfill({ json: { outputs: [] } });
    if (url.pathname === "/api/v1/notebooks/nb_research/chats") return route.fulfill({ json: { chats: [{ id: "chat_research", notebook_id: "nb_research", title: "New chat" }] } });
    if (url.pathname === "/api/v1/chats/chat_research" && method === "GET") return route.fulfill({ json: {
      chat: { id: "chat_research", notebook_id: "nb_research", title: "New chat" }, messages: [], runs: [], citations: [], source_ids: [], research_sessions: []
    } });
    if (url.pathname === "/api/v1/chats/chat_research/messages" && method === "POST") {
      const body = request.postDataJSON() as { id: string; mode: string };
      expect(body.mode).toBe("research");
      messageID = body.id;
      return route.fulfill({ status: 202, json: { message_id: messageID, mode: "research", research_session_id: "research_e2e", run_id: "run_plan", status: "planning" } });
    }
    if (url.pathname === "/api/v1/agent-runs/run_plan/events") return route.fulfill({
      contentType: "text/event-stream",
      body: `event: run\ndata: ${JSON.stringify({ run: { id: "run_plan", input_message_id: messageID, status: "completed" }, message: null })}\n\n`
    });
    if (url.pathname === "/api/v1/research-sessions/research_e2e" && method === "GET") return route.fulfill({ json: {
      session: { id: "research_e2e", chat_id: "chat_research", input_message_id: messageID, status, planning_run_id: "run_plan", ...(status === "queued" ? { accepted_plan_version: planVersion, execution_run_id: "run_research" } : {}) },
      plan: { version: planVersion, content: plan },
      evidence: { discovered: 37, read: 4, failed: 1 }
    } });
    if (url.pathname === "/api/v1/research-sessions/research_e2e/plan" && method === "PATCH") {
      plan = (request.postDataJSON() as { plan: typeof plan }).plan;
      planVersion = 2;
      return route.fulfill({ json: { session_id: "research_e2e", version: planVersion, plan } });
    }
    if (url.pathname === "/api/v1/research-sessions/research_e2e/start" && method === "POST") {
      startedVersion = (request.postDataJSON() as { plan_version: number }).plan_version;
      status = "queued";
      return route.fulfill({ status: 202, json: { session_id: "research_e2e", run_id: "run_research", status: "queued" } });
    }
    return route.fulfill({ status: 404, json: { error: { code: "not_found" } } });
  });

  await page.goto("/notebooks/nb_research");
  if (testInfo.project.name === "chromium-compact") await page.getByRole("tab", { name: "Chat" }).click();
  const chat = testInfo.project.name === "chromium-compact"
    ? page.getByRole("tabpanel", { name: "Chat" })
    : page.getByRole("region", { name: "Chat" });
  await chat.getByRole("button", { name: /Research/ }).click();
  await chat.getByRole("textbox", { name: "Message Nano Notebook" }).fill("Compare Agent Harnesses for a platform team.");
  await chat.getByRole("button", { name: "Send message" }).click();

  const editor = chat.getByRole("textbox", { name: "Research plan" });
  await expect(editor).toBeVisible();
  plan.title = "Edited Agent Harness decision plan";
  await editor.fill(JSON.stringify(plan, null, 2));
  await chat.getByRole("button", { name: "Save plan" }).click();
  await expect.poll(() => planVersion).toBe(2);
  await chat.getByRole("button", { name: "Start research" }).click();
  await expect.poll(() => startedVersion).toBe(2);
  await expect(chat).toContainText("Discovered 37");
  await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true);
  await page.screenshot({ path: testInfo.outputPath("research-mode.png"), fullPage: true });
});
