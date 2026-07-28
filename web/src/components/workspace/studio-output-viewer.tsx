import { useMemo, useState } from "react";
import type { MemberSource } from "./sources";
import type { DataTableArtifact, FlashcardsArtifact, MindMapArtifact, ReportArtifact, StudioOutput } from "./studio-outputs";
import { Button } from "../ui/button";
import { MaterialSymbol } from "../icons/material-symbol";

export type StudioViewerCopy = {
  sourceLabel: string;
  previousLabel: string;
  nextLabel: string;
  flipLabel: string;
  shuffleLabel: string;
  restartLabel: string;
  zoomInLabel: string;
  zoomOutLabel: string;
  missingArtifactLabel: string;
};

export function StudioOutputViewer({ output, sources, onOpenSource, copy }: { output: StudioOutput; sources: MemberSource[]; onOpenSource: (source: MemberSource) => void; copy: StudioViewerCopy }) {
  if (!output.artifact) return <p className="studio-viewer-empty">{copy.missingArtifactLabel}</p>;
  switch (output.kind) {
    case "report": return <ReportViewer artifact={output.artifact as ReportArtifact} sources={sources} onOpenSource={onOpenSource} copy={copy} />;
    case "flashcards": return <FlashcardsViewer artifact={output.artifact as FlashcardsArtifact} sources={sources} onOpenSource={onOpenSource} copy={copy} />;
    case "mind_map": return <MindMapViewer artifact={output.artifact as MindMapArtifact} sources={sources} onOpenSource={onOpenSource} copy={copy} />;
    case "data_table": return <DataTableViewer artifact={output.artifact as DataTableArtifact} sources={sources} onOpenSource={onOpenSource} copy={copy} />;
  }
}

function ReportViewer({ artifact, ...props }: ViewerProps<ReportArtifact>) {
  return <article className="studio-report">
    <p className="studio-report-summary">{artifact.summary}</p>
    {artifact.sections.map((section) => <section key={section.id}>
      <h3>{section.heading}</h3>
      <PlainMarkdown value={section.markdown} />
      <SourceChips sourceIDs={section.source_ids} {...props} />
    </section>)}
  </article>;
}

function PlainMarkdown({ value }: { value: string }) {
  return <div className="studio-markdown">{value.split(/\n{2,}/).map((block, index) => {
    const heading = block.match(/^#{1,6}\s+(.+)$/);
    if (heading) return <h4 key={index}>{heading[1]}</h4>;
    const lines = block.split("\n");
    if (lines.every((line) => /^[-*]\s+/.test(line))) return <ul key={index}>{lines.map((line, lineIndex) => <li key={lineIndex}>{line.replace(/^[-*]\s+/, "")}</li>)}</ul>;
    return <p key={index}>{block}</p>;
  })}</div>;
}

function FlashcardsViewer({ artifact, ...props }: ViewerProps<FlashcardsArtifact>) {
  const [order, setOrder] = useState(() => artifact.cards.map((_, index) => index));
  const [position, setPosition] = useState(0);
  const [flipped, setFlipped] = useState(false);
  const card = artifact.cards[order[position]];
  const move = (next: number) => { setPosition(next); setFlipped(false); };
  const restart = () => { setOrder(artifact.cards.map((_, index) => index)); move(0); };
  const shuffle = () => { setOrder((current) => [...current].sort(() => Math.random() - 0.5)); move(0); };
  return <div className="studio-flashcards">
    <button type="button" className={`studio-flashcard${flipped ? " is-flipped" : ""}`} aria-label={props.copy.flipLabel} onClick={() => setFlipped((value) => !value)}>
      <small>{position + 1} / {artifact.cards.length}</small>
      <strong aria-live="polite">{flipped ? card.back : card.front}</strong>
      <span>{props.copy.flipLabel}</span>
    </button>
    <SourceChips sourceIDs={card.source_ids} {...props} />
    <div className="studio-viewer-controls">
      <Button variant="outline" size="icon" aria-label={props.copy.previousLabel} disabled={position === 0} onClick={() => move(position - 1)}><MaterialSymbol name="chevron_left" /></Button>
      <Button variant="outline" onClick={shuffle}><MaterialSymbol name="shuffle" />{props.copy.shuffleLabel}</Button>
      <Button variant="outline" onClick={restart}><MaterialSymbol name="restart_alt" />{props.copy.restartLabel}</Button>
      <Button variant="outline" size="icon" aria-label={props.copy.nextLabel} disabled={position === order.length - 1} onClick={() => move(position + 1)}><MaterialSymbol name="chevron_right" /></Button>
    </div>
  </div>;
}

function MindMapViewer({ artifact, ...props }: ViewerProps<MindMapArtifact>) {
  const [collapsed, setCollapsed] = useState<Set<string>>(new Set());
  const [zoom, setZoom] = useState(1);
  const children = useMemo(() => {
    const grouped = new Map<string | null, MindMapArtifact["nodes"]>();
    for (const node of artifact.nodes) grouped.set(node.parent_id, [...(grouped.get(node.parent_id) ?? []), node]);
    return grouped;
  }, [artifact.nodes]);
  const toggle = (id: string) => setCollapsed((current) => { const next = new Set(current); if (next.has(id)) next.delete(id); else next.add(id); return next; });
  const renderNodes = (parentID: string | null): React.ReactNode => <ul>{(children.get(parentID) ?? []).map((node) => {
    const hasChildren = children.has(node.id);
    return <li key={node.id}>
      <div className="studio-mind-map-node">
        {hasChildren ? <button type="button" aria-label={node.label} onClick={() => toggle(node.id)}><MaterialSymbol name={collapsed.has(node.id) ? "add" : "remove"} size={16} /></button> : null}
        <strong>{node.label}</strong>{node.detail ? <p>{node.detail}</p> : null}
        <SourceChips sourceIDs={node.source_ids} {...props} />
      </div>
      {!collapsed.has(node.id) && hasChildren ? renderNodes(node.id) : null}
    </li>;
  })}</ul>;
  return <div className="studio-mind-map">
    <div className="studio-viewer-controls">
      <Button variant="outline" size="icon" aria-label={props.copy.zoomOutLabel} disabled={zoom <= .7} onClick={() => setZoom((value) => value - .1)}><MaterialSymbol name="zoom_out" /></Button>
      <span>{Math.round(zoom * 100)}%</span>
      <Button variant="outline" size="icon" aria-label={props.copy.zoomInLabel} disabled={zoom >= 1.4} onClick={() => setZoom((value) => value + .1)}><MaterialSymbol name="zoom_in" /></Button>
    </div>
    <div className="studio-mind-map-canvas" style={{ transform: `scale(${zoom})`, transformOrigin: "top left" }}>{renderNodes(null)}</div>
  </div>;
}

function DataTableViewer({ artifact, ...props }: ViewerProps<DataTableArtifact>) {
  return <div className="studio-data-table">
    {artifact.description ? <p>{artifact.description}</p> : null}
    <div className="studio-table-scroll"><table><thead><tr>{artifact.columns.map((column) => <th key={column} scope="col">{column}</th>)}<th scope="col">{props.copy.sourceLabel}</th></tr></thead>
      <tbody>{artifact.rows.map((row) => <tr key={row.id}>{row.cells.map((cell, index) => <td key={index}>{cell}</td>)}<td><SourceChips sourceIDs={row.source_ids} {...props} /></td></tr>)}</tbody>
    </table></div>
  </div>;
}

type SourceProps = { sources: MemberSource[]; onOpenSource: (source: MemberSource) => void; copy: StudioViewerCopy };
type ViewerProps<T> = SourceProps & { artifact: T };

function SourceChips({ sourceIDs, sources, onOpenSource, copy }: SourceProps & { sourceIDs: string[] }) {
  if (!sourceIDs.length) return null;
  return <div className="studio-source-chips">{sourceIDs.map((id, index) => {
    const source = sources.find((item) => item.id === id);
    return <Button key={id} size="sm" variant="outline" disabled={!source || source.open_action?.kind !== "inline_original"} onClick={() => source && onOpenSource(source)}>{copy.sourceLabel} {index + 1}{source ? ` · ${source.title}` : ""}</Button>;
  })}</div>;
}
