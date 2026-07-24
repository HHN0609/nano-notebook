import { useQuery } from "@tanstack/react-query";
import { csrfToken, memberAPI } from "./source-upload";

export type MemberSource = {
  id: string;
  notebook_id: string;
  title: string;
  format: string;
  byte_size: number;
  state: "processing" | "ready" | "failed";
  failure_reason?: "limits_exceeded" | "source_unavailable" | "content_unreadable" | "indexing_failed" | "retrieval_unavailable" | "processing_interrupted" | "processing_failed";
};

export type SourcesController = {
  sources: MemberSource[];
  selectedSourceIDs: string[];
  isLoading: boolean;
  error: string | null;
  toggle: (sourceID: string) => void;
  refresh: () => Promise<unknown>;
};

export function useNotebookSources(notebookID: string, unavailableLabel: string, chatID?: string, initialSourceIDs: string[] = []): SourcesController {
  const query = useQuery({
    queryKey: ["notebook-sources", notebookID],
    queryFn: async () => {
      const response = await fetch(`/api/v1/notebooks/${notebookID}/sources`, { credentials: "include" });
      if (!response.ok) throw new Error(unavailableLabel);
      return ((await response.json()) as { sources: MemberSource[] }).sources;
    },
    refetchInterval: ({ state }) => state.data?.some((item) => item.state === "processing") ? 2500 : false,
    retry: false
  });
  const selection = useQuery({
    queryKey: ["chat-source-selection", chatID],
    enabled: Boolean(chatID),
    queryFn: async (): Promise<string[]> => {
      const response = await memberAPI(`/api/v1/chats/${chatID}/source-selection`);
      if (!response.ok) throw new Error(unavailableLabel);
      return ((await response.json()) as { source_ids: string[] }).source_ids;
    },
    initialData: initialSourceIDs,
    staleTime: Infinity,
    refetchInterval: query.data?.some((item) => item.state === "processing") ? 2500 : false,
    retry: false
  });
  const selectedSourceIDs = (query.data ?? [])
    .filter((item) => item.state === "ready" && (selection.data ?? []).includes(item.id))
    .map((item) => item.id);

  return {
    sources: query.data ?? [],
    selectedSourceIDs,
    isLoading: query.isLoading,
    error: query.isError || selection.isError ? unavailableLabel : null,
    toggle: (sourceID) => {
      if (!chatID) return;
      const next = selectedSourceIDs.includes(sourceID)
        ? selectedSourceIDs.filter((id) => id !== sourceID)
        : [...selectedSourceIDs, sourceID];
      void memberAPI(`/api/v1/chats/${chatID}/source-selection`, {
        method: "PATCH",
        headers: { "X-CSRF-Token": csrfToken() },
        body: JSON.stringify({ source_ids: next })
      }).then((response) => {
        if (!response.ok) throw new Error(unavailableLabel);
        return selection.refetch();
      }).catch(() => selection.refetch());
    },
    refresh: async () => Promise.all([query.refetch(), selection.refetch()])
  };
}
