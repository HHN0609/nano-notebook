import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { csrfToken, memberAPI } from "./source-upload";

export type StudioOutputKind = "report" | "flashcards" | "mind_map" | "data_table";
export type StudioOutputStatus = "queued" | "running" | "completed" | "failed" | "cancelled";

export type ReportArtifact = {
  title: string;
  summary: string;
  sections: Array<{ id: string; heading: string; markdown: string; source_ids: string[] }>;
};

export type FlashcardsArtifact = {
  title: string;
  cards: Array<{ id: string; front: string; back: string; source_ids: string[] }>;
};

export type MindMapArtifact = {
  title: string;
  nodes: Array<{ id: string; parent_id: string | null; label: string; detail: string; source_ids: string[] }>;
};

export type DataTableArtifact = {
  title: string;
  description: string;
  columns: string[];
  rows: Array<{ id: string; cells: string[]; source_ids: string[] }>;
};

export type StudioOutput = {
  id: string;
  notebook_id: string;
  kind: StudioOutputKind;
  locale: string;
  title: string | null;
  run_id: string;
  source_count: number;
  status: StudioOutputStatus;
  error_code?: string;
  artifact?: ReportArtifact | FlashcardsArtifact | MindMapArtifact | DataTableArtifact;
  created_at: string;
  started_at?: string;
  finished_at?: string;
  updated_at: string;
};

export type StudioOutputsController = {
  outputs: StudioOutput[];
  isLoading: boolean;
  isCreating: boolean;
  error: string | null;
  create: (kind: StudioOutputKind, sourceIDs: string[], locale: string) => Promise<StudioOutput>;
  remove: (outputID: string) => Promise<void>;
  refresh: () => Promise<unknown>;
};

export function useStudioOutputs(notebookID: string, unavailableLabel: string): StudioOutputsController {
  const queryClient = useQueryClient();
  const [isCreating, setIsCreating] = useState(false);
  const queryKey = ["studio-outputs", notebookID] as const;
  const query = useQuery({
    queryKey,
    queryFn: async ({ signal }): Promise<StudioOutput[]> => {
      const response = await memberAPI(`/api/v1/notebooks/${notebookID}/studio-outputs`, { signal });
      if (!response.ok) throw new Error(unavailableLabel);
      return ((await response.json()) as { outputs: StudioOutput[] }).outputs;
    },
    retry: false,
    refetchInterval: ({ state }) => {
      const outputs = state.data as StudioOutput[] | undefined;
      return outputs?.some((output) => output.status === "queued" || output.status === "running") ? 1500 : false;
    }
  });

  return {
    outputs: query.data ?? [],
    isLoading: query.isLoading,
    isCreating,
    error: query.isError ? unavailableLabel : null,
    create: async (kind, sourceIDs, locale) => {
      setIsCreating(true);
      try {
        const response = await memberAPI(`/api/v1/notebooks/${notebookID}/studio-outputs`, {
          method: "POST",
          headers: { "Idempotency-Key": crypto.randomUUID(), "X-CSRF-Token": csrfToken() },
          body: JSON.stringify({ kind, locale, source_ids: sourceIDs })
        });
        if (!response.ok) throw new Error(unavailableLabel);
        const output = ((await response.json()) as { output: StudioOutput }).output;
        queryClient.setQueryData<StudioOutput[]>(queryKey, (current = []) => [output, ...current.filter((item) => item.id !== output.id)]);
        return output;
      } finally {
        setIsCreating(false);
      }
    },
    remove: async (outputID) => {
      const response = await memberAPI(`/api/v1/studio-outputs/${outputID}`, {
        method: "DELETE",
        headers: { "X-CSRF-Token": csrfToken() }
      });
      if (!response.ok) throw new Error(unavailableLabel);
      queryClient.setQueryData<StudioOutput[]>(queryKey, (current = []) => current.filter((item) => item.id !== outputID));
    },
    refresh: query.refetch
  };
}
