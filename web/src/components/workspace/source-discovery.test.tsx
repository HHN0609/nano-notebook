import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, test, vi } from "vitest";
import { SourceDiscovery } from "./source-discovery";

afterEach(() => vi.unstubAllGlobals());

test("searches and renders selected candidates as safe external links with right-side checkboxes", async () => {
  const requests: Array<{ path: string; method: string }> = [];
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const path = String(input);
    const method = init?.method ?? "GET";
    requests.push({ path, method });
    if (method === "GET") return new Response(null, { status: 204 });
    return Response.json({ session: {
      id: "dsc_1", notebook_id: "nb_1", origin: "manual", query: "how to make a film",
      summary: "Practical production guides.", status: "ready", created_at: new Date().toISOString(), updated_at: new Date().toISOString(),
      candidates: [{
        id: "candidate_1", ordinal: 0, title: "Film production guide", canonical_url: "https://example.com/film",
        display_url: "example.com/film", snippet: "A practical guide.", selected: true, status: "discovered"
      }]
    } }, { status: 202 });
  }));
  const onExpandedChange = vi.fn();
  render(<SourceDiscovery
    notebookID="nb_1"
    active
    onExpandedChange={onExpandedChange}
    onImported={vi.fn()}
    copy={{
      label: "Search the web", placeholder: "Search for sources", search: "Search", searching: "Searching…",
      selectAll: "Select all", importSelected: "Import selected", failed: "Search failed", noResults: "No results",
      openResult: "Open result", importFailed: "Import failed", retry: "Retry", imported: "Imported"
    }}
  />);

  const user = userEvent.setup();
  await user.type(screen.getByPlaceholderText("Search for sources"), "how to make a film");
  await user.click(screen.getByRole("button", { name: "Search" }));

  const link = await screen.findByRole("link", { name: /Film production guide/ });
  expect(link).toHaveAttribute("href", "https://example.com/film");
  expect(link).toHaveAttribute("target", "_blank");
  expect(link).toHaveAttribute("rel", "noreferrer noopener");
  expect(screen.getByRole("checkbox", { name: "Film production guide" })).toBeChecked();
  expect(screen.getByRole("checkbox", { name: "Select all" })).toBeChecked();
  expect(screen.getByText("Practical production guides.")).toBeVisible();
  await waitFor(() => expect(onExpandedChange).toHaveBeenCalledWith(true));
  expect(requests.some((request) => request.path === "/api/v1/notebooks/nb_1/source-discovery-sessions" && request.method === "POST")).toBe(true);
});

test("shows a safe error when search admission fails", async () => {
  vi.stubGlobal("fetch", vi.fn(async (_input: RequestInfo | URL, init?: RequestInit) => {
    if ((init?.method ?? "GET") === "GET") return new Response(null, { status: 204 });
    return Response.json({ error: { code: "discovery_not_configured" } }, { status: 503 });
  }));
  render(<SourceDiscovery
    notebookID="nb_1"
    active
    onExpandedChange={vi.fn()}
    onImported={vi.fn()}
    copy={{
      label: "Search the web", placeholder: "Search for sources", search: "Search", searching: "Searching…",
      selectAll: "Select all", importSelected: "Import selected", failed: "Search failed", noResults: "No results",
      openResult: "Open result", importFailed: "Import failed", retry: "Retry", imported: "Imported"
    }}
  />);
  const user = userEvent.setup();
  await user.type(screen.getByPlaceholderText("Search for sources"), "film");
  await user.click(screen.getByRole("button", { name: "Search" }));
  expect(await screen.findByRole("alert")).toHaveTextContent("Search failed");
});

test("loads the exact Research session requested by a completed Leader Run", async () => {
  const requests: string[] = [];
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
    requests.push(String(input));
    return Response.json({ session: {
      id: "dss_research", notebook_id: "nb_1", query: "film lighting", status: "ready",
      summary: "Relevant material", candidates: []
    } });
  }));
  render(<SourceDiscovery
    notebookID="nb_1"
    requestedSessionID="dss_research"
    active
    onExpandedChange={vi.fn()}
    onImported={vi.fn()}
    copy={{
      label: "Search the web", placeholder: "Search for sources", search: "Search", searching: "Searching…",
      selectAll: "Select all", importSelected: "Import selected", failed: "Search failed", noResults: "No results",
      openResult: "Open result", importFailed: "Import failed", retry: "Retry", imported: "Imported"
    }}
  />);
  expect(await screen.findByDisplayValue("film lighting")).toBeInTheDocument();
  expect(requests).toContain("/api/v1/source-discovery-sessions/dss_research");
  expect(requests).not.toContain("/api/v1/notebooks/nb_1/source-discovery-sessions/latest");
});
