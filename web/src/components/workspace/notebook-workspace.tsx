import { useState, type ComponentProps, type ReactNode } from "react";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "../ui/tabs";
import { ChatPanelContent, type ChatPanelCopy } from "./chat-placeholder-panel";
import { usePrivateChat } from "./private-chat";
import { SourcePanelContent, type SourcePanelCopy } from "./source-panel";
import { useNotebookSources, type MemberSource } from "./sources";
import { StudioPanelContent } from "./studio-panel";
import { useStudioOutputs } from "./studio-outputs";

type WorkspacePanelCopy = ChatPanelCopy & Omit<SourcePanelCopy, "title" | "addSourcesLabel" | "emptyTitle" | "emptyBody" | "collapseLabel" | "comingSoonMessage"> & {
  panelsLabel: string;
  sources: string;
  chat: string;
  studio: string;
  addSources: string;
  sourcesEmptyTitle: string;
  sourcesEmptyBody: string;
  collapsePanel: string;
  comingSoon: string;
  studioRecent: string;
  studioNoOutputs: string;
  studioNoSource: string;
  studioQueued: string;
  studioGenerating: string;
  studioFailed: string;
  studioDelete: string;
  studioUnavailable: string;
  studioSource: string;
  studioSourceSingular: string;
  studioSourcePlural: string;
  studioPrevious: string;
  studioNext: string;
  studioFlip: string;
  studioShuffle: string;
  studioRestart: string;
  studioZoomIn: string;
  studioZoomOut: string;
  studioMissingArtifact: string;
  studioActions: ComponentProps<typeof StudioPanelContent>["actions"];
};

export function NotebookWorkspace({ notebookID, copy, canMaintainSources = true }: { notebookID: string; copy: WorkspacePanelCopy; canMaintainSources?: boolean }) {
  const [sourceDiscoveryOpen, setSourceDiscoveryOpen] = useState(false);
  const [viewSourceID, setViewSourceID] = useState<string | null>(null);
  const [compactPanel, setCompactPanel] = useState("sources");
  const chatController = usePrivateChat(notebookID, copy);
  const sourcesController = useNotebookSources(notebookID, copy.sourceUnavailableLabel, chatController.snapshot?.chat.id, chatController.snapshot?.source_ids);
  const studioController = useStudioOutputs(notebookID, copy.studioUnavailable);
  const requestedDiscoverySessionID = chatController.snapshot?.runs.slice().reverse().find((run) => Boolean(run.discovery_session_id))?.discovery_session_id;
  const viewingSource = sourcesController.sources.find((source) => source.id === viewSourceID && source.open_action?.kind === "inline_original");
  const openInlineOriginal = (source: MemberSource) => {
    if (source.open_action?.kind !== "inline_original") return;
    setViewSourceID(source.id);
    setCompactPanel("sources");
  };
  const panels = {
    sources: <SourcePanelContent notebookID={notebookID} originChatID={chatController.snapshot?.chat.id} requestedDiscoverySessionID={requestedDiscoverySessionID} controller={sourcesController} viewingSource={viewingSource} onOpenSource={openInlineOriginal} onCloseSource={() => setViewSourceID(null)} canMaintain={canMaintainSources} onDiscoveryModeChange={setSourceDiscoveryOpen} copy={{ ...copy, title: copy.sources, addSourcesLabel: copy.addSources, emptyTitle: copy.sourcesEmptyTitle, emptyBody: copy.sourcesEmptyBody, collapseLabel: copy.collapsePanel, comingSoonMessage: copy.comingSoon }} />,
    chat: <ChatPanelContent copy={copy} controller={chatController} sources={sourcesController.sources} onOpenSource={openInlineOriginal} selectedSourceCount={sourcesController.selectedSourceIDs.length} />,
    studio: <StudioPanelContent
      title={copy.studio} actions={copy.studioActions} collapseLabel={copy.collapsePanel}
      recentLabel={copy.studioRecent} noOutputsLabel={copy.studioNoOutputs} noSourceLabel={copy.studioNoSource}
      queuedLabel={copy.studioQueued} generatingLabel={copy.studioGenerating} failedLabel={copy.studioFailed}
      deleteLabel={copy.studioDelete} unavailableLabel={copy.studioUnavailable} closeLabel={copy.closeLabel}
      sourceLabel={copy.studioSource} sourceSingularLabel={copy.studioSourceSingular} sourcePluralLabel={copy.studioSourcePlural}
      previousLabel={copy.studioPrevious} nextLabel={copy.studioNext} flipLabel={copy.studioFlip}
      shuffleLabel={copy.studioShuffle} restartLabel={copy.studioRestart} zoomInLabel={copy.studioZoomIn}
      zoomOutLabel={copy.studioZoomOut} missingArtifactLabel={copy.studioMissingArtifact}
      controller={studioController} selectedSourceIDs={sourcesController.selectedSourceIDs} sources={sourcesController.sources}
      canMaintain={canMaintainSources} onOpenSource={openInlineOriginal}
    />
  };

  return (
    <>
      <div className={`workspace-panels${sourceDiscoveryOpen ? " workspace-panels--source-discovery" : ""}`} aria-label={copy.panelsLabel}>
        <WorkspaceRegion id="sources" title={copy.sources}>{panels.sources}</WorkspaceRegion>
        <WorkspaceRegion id="chat" title={copy.chat} chatFramework>{panels.chat}</WorkspaceRegion>
        <WorkspaceRegion id="studio" title={copy.studio}>{panels.studio}</WorkspaceRegion>
      </div>
      <Tabs value={compactPanel} onValueChange={setCompactPanel} className="workspace-compact-tabs">
        <TabsList className="workspace-tabs" aria-label={copy.panelsLabel}>
          <TabsTrigger value="sources">{copy.sources}</TabsTrigger>
          <TabsTrigger value="chat">{copy.chat}</TabsTrigger>
          <TabsTrigger value="studio">{copy.studio}</TabsTrigger>
        </TabsList>
        <WorkspaceTab value="sources">{panels.sources}</WorkspaceTab>
        <WorkspaceTab value="chat">{panels.chat}</WorkspaceTab>
        <WorkspaceTab value="studio">{panels.studio}</WorkspaceTab>
      </Tabs>
    </>
  );
}

function WorkspaceRegion({ id, title, chatFramework = false, children }: { id: string; title: string; chatFramework?: boolean; children: ReactNode }) {
  const titleID = `workspace-${id}-title`;
  return (
    <section className={`workspace-panel workspace-panel--${id}`} role="region" aria-labelledby={titleID} data-chat-framework={chatFramework ? "@assistant-ui/react" : undefined}>
      <span className="sr-only" id={titleID}>{title}</span>
      {children}
    </section>
  );
}

function WorkspaceTab({ value, children }: { value: string; children: ReactNode }) {
  return <TabsContent className="workspace-panel workspace-panel--compact" value={value}>{children}</TabsContent>;
}
