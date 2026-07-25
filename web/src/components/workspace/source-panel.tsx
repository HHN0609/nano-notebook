import { useCallback, useEffect, useRef, useState } from "react";
import { toast } from "sonner";
import { IconButton } from "../icons/icon-button";
import { MaterialSymbol } from "../icons/material-symbol";
import { Button } from "../ui/button";
import { Dialog, DialogContent, DialogDescription, DialogTitle } from "../ui/dialog";
import { Input } from "../ui/input";
import { Label } from "../ui/label";
import { acceptedSourceFormats, csrfToken, memberAPI, uploadSourceFile } from "./source-upload";
import type { MemberSource, SourcesController } from "./sources";
import { SourceDiscovery } from "./source-discovery";
import { SourceOpenTarget } from "./source-open-target";

export type SourcePanelCopy = {
  title: string;
  addSourcesLabel: string;
  discoveryTitle: string;
  emptyTitle: string;
  emptyBody: string;
  collapseLabel: string;
  comingSoonMessage: string;
  addDialogTitle: string;
  addDialogBody: string;
  chooseFilesLabel: string;
  supportedFormatsLabel: string;
  urlLabel: string;
  urlPlaceholder: string;
  addURLLabel: string;
  webSearchLabel: string;
  webSearchPlaceholder: string;
  webSearchActionLabel: string;
  webSearchingLabel: string;
  selectAllLabel: string;
  importSelectedLabel: string;
  webSearchFailedLabel: string;
  noSearchResultsLabel: string;
  openSearchResultLabel: string;
  sourceImportFailedLabel: string;
  readyLabel: string;
  processingLabel: string;
  sourceFailedLabel: string;
  retryLabel: string;
  deleteLabel: string;
  renameLabel: string;
  useSourceLabel: string;
  sourceUnavailableLabel: string;
  uploadFailedLabel: string;
  closeLabel: string;
  backToSourcesLabel: string;
  sourceOriginalLabel: string;
  sourceOriginalUnavailableLabel: string;
  sourcePreviewLabel: string;
  renameDialogTitle: string;
  sourceTitleLabel: string;
  saveLabel: string;
  removeDialogTitle: string;
  removeDialogBody: string;
  removeConfirmLabel: string;
  cancelLabel: string;
  coverageWarningLabel: string;
  failureReasonLabels: Record<NonNullable<MemberSource["failure_reason"]>, string>;
};

export function SourcePanelContent({ copy, notebookID, originChatID, requestedDiscoverySessionID, controller, viewingSource, onOpenSource, onCloseSource, canMaintain = true, onDiscoveryModeChange }: {
  copy: SourcePanelCopy;
  notebookID: string;
  originChatID?: string;
  requestedDiscoverySessionID?: string;
  controller: SourcesController;
  viewingSource?: MemberSource;
  onOpenSource: (source: MemberSource) => void;
  onCloseSource: () => void;
  canMaintain?: boolean;
  onDiscoveryModeChange?: (active: boolean) => void;
}) {
  const fileInput = useRef<HTMLInputElement>(null);
  const [addOpen, setAddOpen] = useState(false);
  const [discoveryOpen, setDiscoveryOpen] = useState(false);
  const [pinnedDiscoverySessionID, setPinnedDiscoverySessionID] = useState<string | undefined>(undefined);
  const [url, setURL] = useState("");
  const [addingURL, setAddingURL] = useState(false);
  const [uploads, setUploads] = useState<Array<{ id: string; title: string; state: "uploading" | "failed" }>>([]);
  const [editingSource, setEditingSource] = useState<{ id: string; title: string } | null>(null);
  const [editTitle, setEditTitle] = useState("");
  const [removingSource, setRemovingSource] = useState<MemberSource | null>(null);
  const openedDiscoverySessionID = useRef<string | undefined>(undefined);
  const activateDiscovery = useCallback(() => setDiscoveryOpen(true), []);
  const ignoreDiscoveryExpansion = useCallback(() => undefined, []);

  useEffect(() => onDiscoveryModeChange?.(discoveryOpen), [discoveryOpen, onDiscoveryModeChange]);

  useEffect(() => {
    if (!requestedDiscoverySessionID || openedDiscoverySessionID.current === requestedDiscoverySessionID || !canMaintain) return;
    openedDiscoverySessionID.current = requestedDiscoverySessionID;
    setPinnedDiscoverySessionID(requestedDiscoverySessionID);
    setDiscoveryOpen(true);
  }, [canMaintain, requestedDiscoverySessionID]);

  async function addFiles(files: FileList | null) {
    if (!files?.length) return;
    const batch = [...files].map((file) => ({ id: crypto.randomUUID(), file }));
    setUploads((current) => [...current, ...batch.map(({ id, file }) => ({ id, title: file.name, state: "uploading" as const }))]);
    await Promise.allSettled(batch.map(async ({ id, file }) => {
      try {
        await uploadSourceFile(notebookID, file);
        setUploads((current) => current.filter((item) => item.id !== id));
      } catch {
        setUploads((current) => current.map((item) => item.id === id ? { ...item, state: "failed" } : item));
      }
    }));
    if (fileInput.current) fileInput.current.value = "";
    await controller.refresh();
  }

  async function addURL() {
    const requestURL = url.trim();
    if (!requestURL || addingURL) return;
    setAddingURL(true);
    try {
      const response = await memberAPI(`/api/v1/notebooks/${notebookID}/sources/urls`, {
        method: "POST",
        headers: { "Idempotency-Key": crypto.randomUUID(), "X-CSRF-Token": csrfToken() },
        body: JSON.stringify({ url: requestURL })
      });
      if (!response.ok) throw new Error(copy.uploadFailedLabel);
      setURL("");
      setAddOpen(false);
      await controller.refresh();
    } catch {
      toast.error(copy.uploadFailedLabel);
    } finally {
      setAddingURL(false);
    }
  }

  async function sourceAction(sourceID: string, action: "retry" | "delete") {
    const path = action === "retry" ? `/api/v1/sources/${sourceID}/retry` : `/api/v1/sources/${sourceID}`;
    const response = await memberAPI(path, {
      method: action === "retry" ? "POST" : "DELETE",
      headers: { "X-CSRF-Token": csrfToken() }
    });
    if (!response.ok) {
      toast.error(copy.sourceUnavailableLabel);
      return;
    }
    await controller.refresh();
  }

  async function renameSource() {
    const title = editTitle.trim();
    if (!editingSource || !title) return;
    const response = await memberAPI(`/api/v1/sources/${editingSource.id}`, {
      method: "PATCH",
      headers: { "X-CSRF-Token": csrfToken() },
      body: JSON.stringify({ title })
    });
    if (!response.ok) {
      toast.error(copy.sourceUnavailableLabel);
      return;
    }
    setEditingSource(null);
    await controller.refresh();
  }

  const statusLabels = { ready: copy.readyLabel, processing: copy.processingLabel, failed: copy.sourceFailedLabel };
  const sourceCollection = (
    <>
      {controller.error ? <p className="source-panel-error" role="alert">{controller.error}</p> : null}
      {!controller.isLoading && controller.sources.length === 0 ? (
        <div className="panel-empty-state">
          <MaterialSymbol name="draft" size={28} />
          <strong>{copy.emptyTitle}</strong>
          <p>{copy.emptyBody}</p>
        </div>
      ) : (
        <div className="source-list">
          {controller.sources.map((source) => (
            <article className="source-list-item" key={source.id}>
              {source.state === "ready" ? (
                <input
                  type="checkbox"
                  aria-label={`${copy.useSourceLabel} ${source.title}`}
                  checked={controller.selectedSourceIDs.includes(source.id)}
                  onChange={() => controller.toggle(source.id)}
                />
              ) : <MaterialSymbol name={source.state === "failed" ? "error" : "hourglass_top"} size={18} />}
              <SourceOpenTarget source={source.state === "ready" ? source : undefined} className="source-list-title" onInlineOriginal={onOpenSource}>{source.title}</SourceOpenTarget>
              <span className={`source-state source-state--${source.state}`}>{statusLabels[source.state]}</span>
              {canMaintain && source.state === "failed" ? <IconButton icon="refresh" label={`${copy.retryLabel} ${source.title}`} onClick={() => void sourceAction(source.id, "retry")} /> : null}
              {canMaintain ? <IconButton icon="edit" label={`${copy.renameLabel} ${source.title}`} onClick={() => { setEditingSource(source); setEditTitle(source.title); }} /> : null}
              {canMaintain ? <IconButton icon="delete" label={`${copy.deleteLabel} ${source.title}`} onClick={() => setRemovingSource(source)} /> : null}
              {source.state === "failed" && source.failure_reason ? <p className="source-failure-reason">{copy.failureReasonLabels[source.failure_reason]}</p> : null}
            </article>
          ))}
        </div>
      )}
    </>
  );

  return (
    <>
    <div className={`workspace-panel-content source-panel-content${viewingSource ? " source-panel-content--hidden" : ""}`} aria-hidden={viewingSource ? true : undefined}>
      <div className="workspace-panel-header">
        <h2>{discoveryOpen ? <span className="source-discovery-breadcrumb"><span>{copy.title}</span><MaterialSymbol name="chevron_right" size={18} /><span>{copy.discoveryTitle}</span></span> : copy.title}</h2>
        {discoveryOpen
          ? <IconButton icon="close" label={copy.closeLabel} symbolSize={19} onClick={() => { setDiscoveryOpen(false); setPinnedDiscoverySessionID(undefined); }} />
          : <IconButton icon="right_panel_close" label={copy.collapseLabel} symbolSize={19} onClick={() => toast(copy.comingSoonMessage)} />}
      </div>
      {canMaintain ? <div className={`source-panel-controls${discoveryOpen ? " source-panel-controls--discovery" : ""}`}>
        <SourceDiscovery
          notebookID={notebookID}
          originChatID={originChatID}
          requestedSessionID={pinnedDiscoverySessionID}
          active
          showResults={discoveryOpen}
          hideLabel
          onExpandedChange={ignoreDiscoveryExpansion}
          onSessionActive={activateDiscovery}
          onImported={controller.refresh}
          onImportAccepted={() => { setDiscoveryOpen(false); setPinnedDiscoverySessionID(undefined); }}
          copy={{
            label: copy.webSearchLabel,
            placeholder: copy.webSearchPlaceholder,
            search: copy.webSearchActionLabel,
            searching: copy.webSearchingLabel,
            selectAll: copy.selectAllLabel,
            importSelected: copy.importSelectedLabel,
            failed: copy.webSearchFailedLabel,
            noResults: copy.noSearchResultsLabel,
            openResult: copy.openSearchResultLabel,
            importFailed: copy.sourceImportFailedLabel,
            retry: copy.retryLabel,
            imported: copy.readyLabel
          }}
        />
        {!discoveryOpen ? <IconButton className="source-add-action" icon="add" label={copy.addSourcesLabel} onClick={() => setAddOpen(true)} /> : null}
      </div> : null}
      {discoveryOpen ? (
        <div className="source-panel-existing-peek">
          <h3>{copy.title}</h3>
          {sourceCollection}
        </div>
      ) : sourceCollection}

      <Dialog open={addOpen} onOpenChange={setAddOpen}>
        <DialogContent className="source-dialog" closeLabel={copy.closeLabel}>
          <DialogTitle>{copy.addDialogTitle}</DialogTitle>
          <DialogDescription>{copy.addDialogBody}</DialogDescription>
          <input ref={fileInput} className="sr-only" type="file" multiple accept={acceptedSourceFormats} aria-label={copy.chooseFilesLabel} onChange={(event) => void addFiles(event.target.files)} />
          <Button variant="outline" onClick={() => fileInput.current?.click()}><MaterialSymbol name="upload_file" size={19} />{copy.chooseFilesLabel}</Button>
          <p className="source-format-help">{copy.supportedFormatsLabel}</p>
          {uploads.length ? <div className="source-upload-list">{uploads.map((item) => <span key={item.id}>{item.title} · {item.state === "failed" ? copy.sourceFailedLabel : copy.processingLabel}</span>)}</div> : null}
          <div className="source-dialog-divider" />
          <Label htmlFor="source-url">{copy.urlLabel}</Label>
          <div className="source-url-row">
            <Input id="source-url" type="url" value={url} placeholder={copy.urlPlaceholder} onChange={(event) => setURL(event.target.value)} />
            <Button disabled={!url.trim() || addingURL} onClick={() => void addURL()}>{copy.addURLLabel}</Button>
          </div>
        </DialogContent>
      </Dialog>

      <Dialog open={Boolean(editingSource)} onOpenChange={(open) => !open && setEditingSource(null)}>
        <DialogContent className="source-dialog" closeLabel={copy.closeLabel}>
          <DialogTitle>{copy.renameDialogTitle}</DialogTitle>
          <Label htmlFor="rename-source-title">{copy.sourceTitleLabel}</Label>
          <Input id="rename-source-title" value={editTitle} onChange={(event) => setEditTitle(event.target.value)} />
          <div className="dialog-actions">
            <Button variant="ghost" onClick={() => setEditingSource(null)}>{copy.cancelLabel}</Button>
            <Button disabled={!editTitle.trim()} onClick={() => void renameSource()}>{copy.saveLabel}</Button>
          </div>
        </DialogContent>
      </Dialog>

      <Dialog open={Boolean(removingSource)} onOpenChange={(open) => !open && setRemovingSource(null)}>
        <DialogContent className="source-dialog" closeLabel={copy.closeLabel}>
          <DialogTitle>{copy.removeDialogTitle}</DialogTitle>
          <DialogDescription>{copy.removeDialogBody}</DialogDescription>
          <div className="dialog-actions">
            <Button variant="ghost" onClick={() => setRemovingSource(null)}>{copy.cancelLabel}</Button>
            <Button variant="destructive" onClick={() => {
              const sourceID = removingSource?.id;
              setRemovingSource(null);
              if (sourceID) void sourceAction(sourceID, "delete");
            }}>{copy.removeConfirmLabel}</Button>
          </div>
        </DialogContent>
      </Dialog>
    </div>
    {viewingSource ? <SourceOriginalViewer key={viewingSource.id} source={viewingSource} onBack={onCloseSource} copy={copy} /> : null}
    </>
  );
}

function SourceOriginalViewer({ source, onBack, copy }: { source: MemberSource; onBack: () => void; copy: SourcePanelCopy }) {
  const [failed, setFailed] = useState(false);
  const action = source.open_action;
  const content = action.kind !== "inline_original" || failed
    ? <p className="source-original-unavailable" role="alert">{copy.sourceOriginalUnavailableLabel}</p>
    : action.media_type.startsWith("image/")
      ? <img src={action.href} alt={source.title} onError={() => setFailed(true)} />
      : action.media_type.startsWith("audio/")
        ? <audio src={action.href} controls aria-label={source.title} onError={() => setFailed(true)} />
        : <iframe src={action.href} title={source.title} onError={() => setFailed(true)} />;
  return (
    <div className="workspace-panel-content source-panel-content source-original-viewer">
      <div className="workspace-panel-header">
        <h2 className="source-original-breadcrumb"><span>{copy.title}</span><MaterialSymbol name="chevron_right" size={18} /><span>{source.title}</span></h2>
        <IconButton icon="arrow_back" label={copy.backToSourcesLabel} symbolSize={19} onClick={onBack} />
      </div>
      <section className="source-original-viewer-body" role="region" aria-label={`${copy.sourceOriginalLabel} ${source.title}`}>
        {content}
      </section>
    </div>
  );
}
