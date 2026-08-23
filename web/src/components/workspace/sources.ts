import { useEffect } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { csrfToken, memberAPI } from "./source-upload";

export type SourceOpenAction =
  | { kind: "external"; href: string }
  | { kind: "inline_original"; href: string; media_type: string }
  | { kind: "none" };

export type MemberSource = {
  id: string;
  notebook_id: string;
  title: string;
  format: string;
  byte_size: number;
  state: "processing" | "ready" | "failed";
  open_action: SourceOpenAction;
  failure_reason?: "limits_exceeded" | "source_unavailable" | "content_unreadable" | "indexing_failed" | "retrieval_unavailable" | "processing_interrupted" | "processing_failed";
  admission?: SourceAdmissionSummary;
};

export type SourceAdmissionSummary = {
  report_id: string;
  status: "passed" | "review_required" | "not_applicable";
  score?: number;
  signal_coverage: number;
  exact_identity_match: boolean;
  policy_id: string;
  policy_sha256: string;
  mode: "shadow" | "enforcement";
  review_decision?: "approve" | "reject";
};

export type SourceAdmissionReason =
  | "extraction_complete"
  | "extraction_partial"
  | "exact_url_match"
  | "exact_identifier_match"
  | "external_reference_found"
  | "external_verification_unavailable"
  | "external_verification_not_applicable"
  | "exact_identity_required"
  | "signal_coverage_insufficient"
  | "score_below_threshold";

export type SourceAdmissionDetail = {
  source_id: string;
  notebook_id: string;
  revision_id: string;
  mode: "shadow" | "enforcement";
  report: {
    id: string;
    policy_id: string;
    policy_sha256: string;
    status: SourceAdmissionSummary["status"];
    score?: number;
    signal_coverage: number;
    exact_identity_match: boolean;
    components: Record<string, number>;
    reasons: SourceAdmissionReason[];
  };
  input: {
    provider_id?: string;
    provider_attempts: number;
    searches?: Array<{ query: string; results: Array<{ title: string; url: string; rank: number }> }>;
  };
  provider_id?: string;
  provider_attempts: number;
  review?: { id: string; report_id: string; decision: "approve" | "reject"; note?: string; created_at: string };
  created_at: string;
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
  const queryClient = useQueryClient();
  const query = useQuery({
    queryKey: ["notebook-sources", notebookID],
    queryFn: async ({ signal }) => {
      const response = await fetch(`/api/v1/notebooks/${notebookID}/sources`, { credentials: "include", signal });
      if (!response.ok) throw new Error(unavailableLabel);
      return ((await response.json()) as { sources: MemberSource[] }).sources;
    },
    retry: false
  });
  const selection = useQuery({
    queryKey: ["chat-source-selection", chatID],
    enabled: Boolean(chatID),
    queryFn: async ({ signal }): Promise<string[]> => {
      const response = await memberAPI(`/api/v1/chats/${chatID}/source-selection`, { signal });
      if (!response.ok) throw new Error(unavailableLabel);
      return ((await response.json()) as { source_ids: string[] }).source_ids;
    },
    initialData: initialSourceIDs,
    staleTime: Infinity,
    retry: false
  });
  useEffect(() => {
    if (!notebookID || typeof EventSource === "undefined") return;
    const chatQuery = chatID ? `?chat_id=${encodeURIComponent(chatID)}` : "";
    const events = new EventSource(`/api/v1/notebooks/${notebookID}/sources/events${chatQuery}`);
    const projectSources = (event: Event) => {
      try {
        const payload = JSON.parse((event as MessageEvent<string>).data) as { sources?: MemberSource[]; source_ids?: string[] };
        if (!Array.isArray(payload.sources) || !Array.isArray(payload.source_ids)) return;
        void queryClient.cancelQueries({ queryKey: ["notebook-sources", notebookID], exact: true });
        queryClient.setQueryData(["notebook-sources", notebookID], payload.sources);
        if (chatID) {
          void queryClient.cancelQueries({ queryKey: ["chat-source-selection", chatID], exact: true });
          queryClient.setQueryData(["chat-source-selection", chatID], payload.source_ids);
        }
      } catch {
        // A malformed projection is ignored; EventSource will keep the stream alive.
      }
    };
    events.addEventListener("sources", projectSources);
    return () => events.close();
  }, [chatID, notebookID, queryClient]);
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
