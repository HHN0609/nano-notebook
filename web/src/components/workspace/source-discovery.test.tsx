import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, test, vi } from "vitest";
import { SourceDiscovery } from "./source-discovery";

afterEach(() => vi.unstubAllGlobals());

test("renders a compact Research card with three previews until View is chosen", async () => {
  const candidates = Array.from({ length: 5 }, (_, index) => ({
    id: `candidate_${index + 1}`,
    ordinal: index,
    title: `Research source ${index + 1}`,
    canonical_url: `https://example.com/source-${index + 1}`,
    display_url: `example.com/source-${index + 1}`,
    snippet: `Summary ${index + 1}.`,
    selected: true,
    status: "discovered"
  }));
  vi.stubGlobal("fetch", vi.fn(async () => Response.json({ session: {
    id: "dsc_compact", notebook_id: "nb_1", query: "compact research", status: "ready", candidates
  } })));
  const onViewResults = vi.fn();

  render(<SourceDiscovery
    notebookID="nb_1"
    active
    detailOpen={false}
    onViewResults={onViewResults}
    onExpandedChange={vi.fn()}
    onImported={vi.fn()}
    copy={{
      label: "Search the web", placeholder: "Search for sources", search: "Search", searching: "Searching…",
      selectAll: "Select all", importSelected: "Import selected", failed: "Search failed", noResults: "No results",
      openResult: "Open result", importFailed: "Import failed", retry: "Retry", imported: "Imported",
      researchComplete: "Research completed", viewResults: "View", moreSources: "{count} more sources"
    }}
  />);

  expect(await screen.findByText("Research completed")).toBeVisible();
  expect(screen.getByRole("link", { name: /Research source 1/ })).toBeVisible();
  expect(screen.getByRole("link", { name: /Research source 3/ })).toBeVisible();
  expect(screen.queryByRole("link", { name: /Research source 4/ })).not.toBeInTheDocument();
  expect(screen.getByText("2 more sources")).toBeVisible();
  expect(screen.queryByRole("checkbox", { name: "Select all" })).not.toBeInTheDocument();

  await userEvent.setup().click(screen.getByRole("button", { name: "View" }));
  expect(onViewResults).toHaveBeenCalledTimes(1);
});

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
      openResult: "Open result", importFailed: "Import failed", retry: "Retry", imported: "Imported",
      researchComplete: "Research completed", viewResults: "View", moreSources: "{count} more sources"
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
	const icon = link.closest(".source-discovery-result")?.querySelector<HTMLImageElement>(".source-discovery-site-icon img");
	expect(icon).toHaveAttribute("src", "https://example.com/favicon.ico");
	fireEvent.error(icon!);
	expect(link.closest(".source-discovery-result")?.querySelector(".material-symbol")).toHaveTextContent("language");
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
      openResult: "Open result", importFailed: "Import failed", retry: "Retry", imported: "Imported",
      researchComplete: "Research completed", viewResults: "View", moreSources: "{count} more sources"
    }}
  />);
  const user = userEvent.setup();
  await user.type(screen.getByPlaceholderText("Search for sources"), "film");
  await user.click(screen.getByRole("button", { name: "Search" }));
  expect(await screen.findByRole("alert")).toHaveTextContent("Search failed");
});

test("does not render failed import candidates as Source choices", async () => {
  vi.stubGlobal("fetch", vi.fn(async () => Response.json({ session: {
    id: "dsc_status", notebook_id: "nb_1", query: "film", status: "ready",
    candidates: [
      { id: "candidate_ready", ordinal: 0, title: "Imported guide", canonical_url: "https://example.com/ready", display_url: "example.com/ready", snippet: "Ready.", selected: true, status: "imported" },
      { id: "candidate_failed", ordinal: 1, title: "Failed guide", canonical_url: "https://example.com/failed", display_url: "example.com/failed", snippet: "Failed.", selected: true, status: "import_failed" }
    ]
  } })));
  render(<SourceDiscovery
    notebookID="nb_1"
    active
    onExpandedChange={vi.fn()}
    onImported={vi.fn()}
    copy={{
      label: "Search the web", placeholder: "Search for sources", search: "Search", searching: "Searching…",
      selectAll: "Select all", importSelected: "Import selected", failed: "Search failed", noResults: "No results",
      openResult: "Open result", importFailed: "Import failed", retry: "Retry", imported: "Ready",
      researchComplete: "Research completed", viewResults: "View", moreSources: "{count} more sources"
    }}
  />);

  expect(await screen.findByText("Ready")).toHaveClass("source-discovery-imported");
  expect(screen.queryByText("Failed guide")).not.toBeInTheDocument();
  expect(screen.queryByText("Import failed")).not.toBeInTheDocument();
});

test("notifies its owner to collapse after at least one Source is admitted", async () => {
  let reads = 0;
  vi.stubGlobal("fetch", vi.fn(async (_input: RequestInfo | URL, init?: RequestInit) => {
    if ((init?.method ?? "GET") === "POST") return Response.json({ outcomes: [{ candidate_id: "candidate_1", status: "imported", source_id: "src_1" }] }, { status: 202 });
    reads += 1;
    return Response.json({ session: {
      id: "dsc_import", notebook_id: "nb_1", query: "film", status: "ready",
      candidates: [{ id: "candidate_1", ordinal: 0, title: "Guide", canonical_url: "https://example.com/guide", display_url: "example.com/guide", snippet: "Guide.", selected: true, status: reads > 1 ? "imported" : "discovered" }]
    } });
  }));
  const onImportAccepted = vi.fn();
  render(<SourceDiscovery
    notebookID="nb_1"
    active
    detailOpen={false}
    onExpandedChange={vi.fn()}
    onImported={vi.fn()}
    onImportAccepted={onImportAccepted}
    copy={{
      label: "Search the web", placeholder: "Search for sources", search: "Search", searching: "Searching…",
      selectAll: "Select all", importSelected: "Import selected", failed: "Search failed", noResults: "No results",
      openResult: "Open result", importFailed: "Import failed", retry: "Retry", imported: "Imported",
      researchComplete: "Research completed", viewResults: "View", moreSources: "{count} more sources"
    }}
  />);
  const user = userEvent.setup();
  await user.click(await screen.findByRole("button", { name: "Import selected" }));
  await waitFor(() => expect(onImportAccepted).toHaveBeenCalledTimes(1));
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
      openResult: "Open result", importFailed: "Import failed", retry: "Retry", imported: "Imported",
      researchComplete: "Research completed", viewResults: "View", moreSources: "{count} more sources"
    }}
  />);
  expect(await screen.findByDisplayValue("film lighting")).toBeInTheDocument();
  expect(requests).toContain("/api/v1/source-discovery-sessions/dss_research");
  expect(requests).not.toContain("/api/v1/notebooks/nb_1/source-discovery-sessions/latest");
});

test("does not reactivate Discovery when clearing the pinned session reveals an already imported latest session", async () => {
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
    const isLatest = String(input).endsWith("/latest");
    return Response.json({ session: {
      id: isLatest ? "dss_latest" : "dss_research",
      notebook_id: "nb_1",
      query: "film lighting",
      status: "ready",
      candidates: [{
        id: "candidate_1",
        ordinal: 0,
        title: "Film lighting guide",
        canonical_url: "https://example.com/lighting",
        display_url: "example.com/lighting",
        snippet: "Relevant material.",
        selected: true,
        status: isLatest ? "imported" : "discovered"
      }]
    } });
  }));
  const onSessionActive = vi.fn();
  const props = {
    notebookID: "nb_1",
    active: true,
    onExpandedChange: vi.fn(),
    onSessionActive,
    onImported: vi.fn(),
    copy: {
      label: "Search the web", placeholder: "Search for sources", search: "Search", searching: "Searching…",
      selectAll: "Select all", importSelected: "Import selected", failed: "Search failed", noResults: "No results",
      openResult: "Open result", importFailed: "Import failed", retry: "Retry", imported: "Imported",
      researchComplete: "Research completed", viewResults: "View", moreSources: "{count} more sources"
    }
  };
  const { rerender } = render(<SourceDiscovery {...props} requestedSessionID="dss_research" />);

  await waitFor(() => expect(onSessionActive).toHaveBeenCalledTimes(1));
  rerender(<SourceDiscovery {...props} requestedSessionID={undefined} />);

  await waitFor(() => expect(fetch).toHaveBeenCalledWith("/api/v1/notebooks/nb_1/source-discovery-sessions/latest", expect.anything()));
  expect(onSessionActive).toHaveBeenCalledTimes(1);
});
