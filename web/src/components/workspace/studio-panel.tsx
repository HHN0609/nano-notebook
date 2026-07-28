import { useState } from "react";
import { toast } from "sonner";
import { IconButton } from "../icons/icon-button";
import { MaterialSymbol } from "../icons/material-symbol";
import { Button } from "../ui/button";
import { Dialog, DialogContent, DialogDescription, DialogTitle } from "../ui/dialog";
import type { MemberSource } from "./sources";
import { StudioOutputViewer, type StudioViewerCopy } from "./studio-output-viewer";
import type { StudioOutput, StudioOutputKind, StudioOutputsController } from "./studio-outputs";

export type StudioAction = { kind: StudioOutputKind; icon: string; label: string; tone: string };

export type StudioPanelCopy = StudioViewerCopy & {
  title: string;
  actions: StudioAction[];
  collapseLabel: string;
  recentLabel: string;
  noOutputsLabel: string;
  noSourceLabel: string;
  generatingLabel: string;
  queuedLabel: string;
  failedLabel: string;
  deleteLabel: string;
  unavailableLabel: string;
  closeLabel: string;
};

type StudioPanelProps = StudioPanelCopy & {
  controller: StudioOutputsController;
  selectedSourceIDs: string[];
  sources: MemberSource[];
  canMaintain: boolean;
  onOpenSource: (source: MemberSource) => void;
};

export function StudioPanelContent(props: StudioPanelProps) {
  const [viewing, setViewing] = useState<StudioOutput | null>(null);
  const actionByKind = new Map(props.actions.map((action) => [action.kind, action]));
  const create = async (kind: StudioOutputKind) => {
    if (!props.selectedSourceIDs.length) return toast.info(props.noSourceLabel);
    const locale = document.documentElement.lang === "zh-CN" ? "zh-CN" : "en";
    try {
      await props.controller.create(kind, props.selectedSourceIDs, locale);
    } catch {
      toast.error(props.unavailableLabel);
    }
  };
  const remove = async (outputID: string) => {
    try {
      await props.controller.remove(outputID);
      if (viewing?.id === outputID) setViewing(null);
    } catch {
      toast.error(props.unavailableLabel);
    }
  };

  return <div className="workspace-panel-content studio-panel-content">
    <div className="workspace-panel-header">
      <h2>{props.title}</h2>
      <IconButton icon="left_panel_close" label={props.collapseLabel} symbolSize={19} disabled />
    </div>
    <div className="studio-action-grid">
      {props.actions.map((action) => <Button className="studio-action-card" data-tone={action.tone} disabled={!props.canMaintain || props.controller.isCreating} key={action.kind} variant="ghost" onClick={() => void create(action.kind)}>
        <MaterialSymbol name={action.icon} size={20} /><span>{action.label}</span><MaterialSymbol className="studio-action-arrow" name="add" size={19} />
      </Button>)}
    </div>
    <div className="studio-recent">
      <h3>{props.recentLabel}</h3>
      {props.controller.isLoading ? <p className="studio-recent-empty">{props.queuedLabel}</p> : null}
      {!props.controller.isLoading && !props.controller.outputs.length ? <div className="studio-empty-state"><MaterialSymbol name="auto_awesome" size={28} /><p>{props.noOutputsLabel}</p></div> : null}
      {props.controller.outputs.map((output) => {
        const action = actionByKind.get(output.kind);
        const label = output.title || (output.artifact as { title?: string } | undefined)?.title || action?.label || output.kind;
        const pending = output.status === "queued" || output.status === "running";
        const status = pending ? props.generatingLabel : output.status === "failed" ? props.failedLabel : "";
        return <div className="studio-output-row" data-status={output.status} key={output.id}>
          <Button aria-label={label} className="studio-output-open" variant="ghost" disabled={output.status !== "completed" || !output.artifact} onClick={() => setViewing(output)}>
            <span className="studio-output-icon" data-tone={action?.tone}><MaterialSymbol name={pending ? "progress_activity" : action?.icon ?? "description"} size={20} /></span>
            <span className="studio-output-copy"><strong>{label}</strong><small>{status || new Date(output.created_at).toLocaleDateString()}</small></span>
          </Button>
          {props.canMaintain ? <IconButton className="studio-output-delete" icon="delete" label={`${props.deleteLabel} ${label}`} symbolSize={18} onClick={() => void remove(output.id)} /> : null}
        </div>;
      })}
      {props.controller.error ? <p className="studio-recent-error">{props.controller.error}</p> : null}
    </div>
    <Dialog open={Boolean(viewing)} onOpenChange={(open) => { if (!open) setViewing(null); }}>
      {viewing ? <DialogContent className="studio-viewer-dialog" closeLabel={props.closeLabel}>
        <DialogTitle>{viewing.title || (viewing.artifact as { title?: string } | undefined)?.title || actionByKind.get(viewing.kind)?.label}</DialogTitle>
        <DialogDescription>{actionByKind.get(viewing.kind)?.label} · {viewing.source_count} {props.sourceLabel.toLowerCase()}</DialogDescription>
        <div className="studio-viewer-body"><StudioOutputViewer output={viewing} sources={props.sources} onOpenSource={props.onOpenSource} copy={props} /></div>
      </DialogContent> : null}
    </Dialog>
  </div>;
}
