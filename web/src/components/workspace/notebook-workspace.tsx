import { useState, type ComponentProps, type ReactNode } from "react";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "../ui/tabs";
import { ChatPanelContent, type ChatPanelCopy } from "./chat-placeholder-panel";
import { usePrivateChat } from "./private-chat";
import { SourcePanelContent, type SourcePanelCopy } from "./source-panel";
import { useNotebookSources } from "./sources";
import { StudioPanelContent } from "./studio-panel";

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
  beta: string;
  studioEmptyTitle: string;
  studioEmptyBody: string;
  addNote: string;
  studioActions: ComponentProps<typeof StudioPanelContent>["actions"];
};

export function NotebookWorkspace({ notebookID, copy, canMaintainSources = true }: { notebookID: string; copy: WorkspacePanelCopy; canMaintainSources?: boolean }) {
  const [sourceDiscoveryOpen, setSourceDiscoveryOpen] = useState(false);
  const chatController = usePrivateChat(notebookID, copy);
  const sourcesController = useNotebookSources(notebookID, copy.sourceUnavailableLabel, chatController.snapshot?.chat.id, chatController.snapshot?.source_ids);
  const requestedDiscoverySessionID = chatController.snapshot?.runs.slice().reverse().find((run) => Boolean(run.discovery_session_id))?.discovery_session_id;
  const panels = {
    sources: <SourcePanelContent notebookID={notebookID} originChatID={chatController.snapshot?.chat.id} requestedDiscoverySessionID={requestedDiscoverySessionID} controller={sourcesController} canMaintain={canMaintainSources} onDiscoveryModeChange={setSourceDiscoveryOpen} copy={{ ...copy, title: copy.sources, addSourcesLabel: copy.addSources, emptyTitle: copy.sourcesEmptyTitle, emptyBody: copy.sourcesEmptyBody, collapseLabel: copy.collapsePanel, comingSoonMessage: copy.comingSoon }} />,
    chat: <ChatPanelContent copy={copy} controller={chatController} selectedSourceCount={sourcesController.selectedSourceIDs.length} />,
    studio: <StudioPanelContent title={copy.studio} actions={copy.studioActions} betaLabel={copy.beta} emptyTitle={copy.studioEmptyTitle} emptyBody={copy.studioEmptyBody} addNoteLabel={copy.addNote} collapseLabel={copy.collapsePanel} comingSoonMessage={copy.comingSoon} />
  };

  return (
    <>
      <div className={`workspace-panels${sourceDiscoveryOpen ? " workspace-panels--source-discovery" : ""}`} aria-label={copy.panelsLabel}>
        <WorkspaceRegion id="sources" title={copy.sources}>{panels.sources}</WorkspaceRegion>
        <WorkspaceRegion id="chat" title={copy.chat} chatFramework>{panels.chat}</WorkspaceRegion>
        <WorkspaceRegion id="studio" title={copy.studio}>{panels.studio}</WorkspaceRegion>
      </div>
      <Tabs defaultValue="sources" className="workspace-compact-tabs">
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
