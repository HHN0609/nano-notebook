import { act, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, expect, test, vi } from "vitest";
import { useNotebookSources } from "./sources";

class SourcesEventSource {
  static instances: SourcesEventSource[] = [];
  readonly url: string;
  readonly listeners = new Map<string, Set<EventListener>>();
  closed = false;

  constructor(url: string | URL) {
    this.url = String(url);
    SourcesEventSource.instances.push(this);
  }

  addEventListener(type: string, listener: EventListener) {
    const listeners = this.listeners.get(type) ?? new Set<EventListener>();
    listeners.add(listener);
    this.listeners.set(type, listeners);
  }

  close() { this.closed = true; }

  emit(type: string, data: unknown) {
    const event = new MessageEvent(type, { data: JSON.stringify(data) });
    for (const listener of this.listeners.get(type) ?? []) listener(event);
  }
}

afterEach(() => vi.unstubAllGlobals());

function SourcesHarness({ notebookID = "nb_events", chatID = "chat_events" }: { notebookID?: string; chatID?: string }) {
  const controller = useNotebookSources(notebookID, "Unavailable", chatID);
  return <div>{controller.sources.map((source) => <span key={source.id}>{source.title}:{source.state}</span>)}<output>{controller.selectedSourceIDs.join(",")}</output></div>;
}

test("projects Notebook Sources and Chat selection through SSE without refetch polling", async () => {
  SourcesEventSource.instances = [];
  vi.stubGlobal("EventSource", SourcesEventSource);
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
    if (String(input).endsWith("/sources")) return Response.json({ sources: [] });
    return Response.json({ source_ids: [] });
  }));
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const { unmount } = render(<QueryClientProvider client={queryClient}><SourcesHarness /></QueryClientProvider>);

  await waitFor(() => expect(SourcesEventSource.instances).toHaveLength(1));
  expect(SourcesEventSource.instances[0]?.url).toBe("/api/v1/notebooks/nb_events/sources/events?chat_id=chat_events");
  act(() => SourcesEventSource.instances[0]?.emit("sources", {
    sources: [{ id: "src_event", notebook_id: "nb_events", title: "SSE Source", format: "txt", byte_size: 4, state: "ready", open_action: { kind: "none" } }],
    source_ids: ["src_event"]
  }));
  expect(await screen.findByText("SSE Source:ready")).toBeVisible();
  expect(screen.getByText("src_event")).toBeVisible();
  unmount();
  expect(SourcesEventSource.instances[0]?.closed).toBe(true);
});

test("closes and replaces the Sources stream when its projection identity changes", async () => {
  SourcesEventSource.instances = [];
  vi.stubGlobal("EventSource", SourcesEventSource);
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
    if (String(input).endsWith("/sources")) return Response.json({ sources: [] });
    return Response.json({ source_ids: [] });
  }));
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const { rerender, unmount } = render(<QueryClientProvider client={queryClient}><SourcesHarness notebookID="nb_first" chatID="chat_first" /></QueryClientProvider>);
  await waitFor(() => expect(SourcesEventSource.instances).toHaveLength(1));

  rerender(<QueryClientProvider client={queryClient}><SourcesHarness notebookID="nb_second" chatID="chat_second" /></QueryClientProvider>);
  await waitFor(() => expect(SourcesEventSource.instances).toHaveLength(2));
  expect(SourcesEventSource.instances[0]?.closed).toBe(true);
  expect(SourcesEventSource.instances[1]?.url).toBe("/api/v1/notebooks/nb_second/sources/events?chat_id=chat_second");
  unmount();
  expect(SourcesEventSource.instances[1]?.closed).toBe(true);
});
