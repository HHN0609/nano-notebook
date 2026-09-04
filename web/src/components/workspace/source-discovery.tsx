import { useEffect, useId, useMemo, useState } from "react";
import { Button } from "../ui/button";
import { Input } from "../ui/input";
import { Label } from "../ui/label";
import { MaterialSymbol } from "../icons/material-symbol";
import { csrfToken, memberAPI } from "./source-upload";
import { SourceSiteIcon } from "./source-site-icon";

type DiscoveryCandidate = {
  id: string;
  ordinal: number;
  title: string;
  canonical_url: string;
  display_url: string;
  snippet: string;
  favicon_ref?: string;
  selected: boolean;
  status: "discovered" | "importing" | "imported" | "import_failed";
  source_id?: string;
  import_error_code?: string;
};

type DiscoverySession = {
  id: string;
  notebook_id: string;
  query: string;
  summary?: string;
  status: "searching" | "ready" | "failed";
  error_code?: string;
  candidates: DiscoveryCandidate[];
};

function shouldActivateDiscovery(session: DiscoverySession) {
  return session.status === "ready"
    && session.candidates.some((candidate) => candidate.status === "discovered" || candidate.status === "importing");
}

export type SourceDiscoveryCopy = {
  label: string;
  placeholder: string;
  search: string;
  searching: string;
  selectAll: string;
  importSelected: string;
  failed: string;
  noResults: string;
  openResult: string;
  importFailed: string;
  retry: string;
  imported: string;
  researchComplete: string;
  viewResults: string;
  moreSources: string;
};

export function SourceDiscovery({ notebookID, originChatID, requestedSessionID, active, showResults = true, detailOpen = showResults, hideLabel = false, copy, onExpandedChange, onSessionActive, onViewResults, onImported, onImportAccepted }: {
  notebookID: string;
  originChatID?: string;
  requestedSessionID?: string;
  active: boolean;
  showResults?: boolean;
  detailOpen?: boolean;
  hideLabel?: boolean;
  copy: SourceDiscoveryCopy;
  onExpandedChange: (expanded: boolean) => void;
  onSessionActive?: () => void;
  onViewResults?: () => void;
  onImported: () => void | Promise<unknown>;
  onImportAccepted?: () => void;
}) {
  const searchInputID = useId();
  const [query, setQuery] = useState("");
  const [session, setSession] = useState<DiscoverySession | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const visibleCandidates = useMemo(() => (session?.candidates.filter((candidate) => candidate.status !== "import_failed") ?? []).sort((left, right) => left.ordinal - right.ordinal), [session]);
  const hasResults = session?.status === "ready" && visibleCandidates.length > 0;
  const previewCandidates = visibleCandidates.slice(0, 3);
  const remainingCandidateCount = Math.max(0, visibleCandidates.length - previewCandidates.length);

  useEffect(() => onExpandedChange(Boolean(showResults && detailOpen && hasResults)), [detailOpen, hasResults, onExpandedChange, showResults]);
  useEffect(() => {
    if (!active) {
      onExpandedChange(false);
      return;
    }
    let cancelled = false;
    const path = requestedSessionID
      ? `/api/v1/source-discovery-sessions/${requestedSessionID}`
      : `/api/v1/notebooks/${notebookID}/source-discovery-sessions/latest`;
    void memberAPI(path).then(async (response) => {
      if (cancelled || response.status === 204 || !response.ok) return;
      const payload = await response.json() as { session: DiscoverySession };
      if (!cancelled) {
        setSession(payload.session);
        setQuery(payload.session.query);
        if (shouldActivateDiscovery(payload.session)) onSessionActive?.();
      }
    });
    return () => { cancelled = true; };
  }, [active, notebookID, onExpandedChange, onSessionActive, requestedSessionID]);

  useEffect(() => {
    if (!active || !session?.id || typeof EventSource === "undefined") return;
    const sessionID = session.id;
    const events = new EventSource(`/api/v1/source-discovery-sessions/${sessionID}/events`);
    const projectSession = (event: Event) => {
      try {
        const payload = JSON.parse((event as MessageEvent<string>).data) as { session?: DiscoverySession };
        if (!payload.session || payload.session.id !== sessionID) return;
        setSession(payload.session);
        setQuery(payload.session.query);
        if (shouldActivateDiscovery(payload.session)) onSessionActive?.();
      } catch {
        // A malformed projection is ignored; EventSource will keep the stream alive.
      }
    };
    events.addEventListener("discovery", projectSession);
    return () => events.close();
  }, [active, onSessionActive, session?.id]);

  async function search() {
    const value = query.trim();
    if (!value || busy) return;
    setBusy(true);
    setError(null);
    try {
      const response = await memberAPI(`/api/v1/notebooks/${notebookID}/source-discovery-sessions`, {
        method: "POST",
        headers: { "X-CSRF-Token": csrfToken() },
        body: JSON.stringify({ query: value, ...(originChatID ? { origin_chat_id: originChatID } : {}) })
      });
      if (!response.ok) throw new Error(copy.failed);
      setSession(((await response.json()) as { session: DiscoverySession }).session);
      onSessionActive?.();
    } catch {
      setError(copy.failed);
    } finally {
      setBusy(false);
    }
  }

  async function retrySearch() {
    if (!session || busy) return;
    setBusy(true);
    setError(null);
    try {
      const response = await memberAPI(`/api/v1/source-discovery-sessions/${session.id}/retry`, {
        method: "POST",
        headers: { "Idempotency-Key": crypto.randomUUID(), "X-CSRF-Token": csrfToken() }
      });
      if (!response.ok) throw new Error(copy.failed);
      const payload = await response.json() as { session?: DiscoverySession };
      if (payload.session) setSession(payload.session);
    } catch {
      setError(copy.failed);
    } finally {
      setBusy(false);
    }
  }

  async function replaceSelection(candidateIDs: string[]) {
    if (!session || busy) return;
    setBusy(true);
    setError(null);
    try {
      const response = await memberAPI(`/api/v1/source-discovery-sessions/${session.id}/selection`, {
        method: "PATCH",
        headers: { "X-CSRF-Token": csrfToken() },
        body: JSON.stringify({ candidate_ids: candidateIDs })
      });
      if (!response.ok) throw new Error(copy.failed);
      setSession(((await response.json()) as { session: DiscoverySession }).session);
    } catch {
      setError(copy.failed);
    } finally {
      setBusy(false);
    }
  }

  const importable = useMemo(() => visibleCandidates.filter((candidate) => candidate.status === "discovered"), [visibleCandidates]);
  const selectedIDs = useMemo(() => importable.filter((candidate) => candidate.selected).map((candidate) => candidate.id), [importable]);
  const allSelected = importable.length > 0 && selectedIDs.length === importable.length;

  async function importSelected() {
    if (!session || selectedIDs.length === 0 || busy) return;
    setBusy(true);
    setError(null);
    try {
      const response = await memberAPI(`/api/v1/source-discovery-sessions/${session.id}/imports`, {
        method: "POST",
        headers: { "Idempotency-Key": crypto.randomUUID(), "X-CSRF-Token": csrfToken() }
      });
      if (!response.ok) throw new Error(copy.importFailed);
      const payload = await response.json() as { outcomes?: Array<{ status: string }> };
      const refreshed = await memberAPI(`/api/v1/source-discovery-sessions/${session.id}`);
      if (refreshed.ok) setSession(((await refreshed.json()) as { session: DiscoverySession }).session);
      await onImported();
      if (payload.outcomes?.some((outcome) => outcome.status === "imported")) onImportAccepted?.();
    } catch {
      setError(copy.importFailed);
    } finally {
      setBusy(false);
    }
  }

  return <section className="source-discovery">
    <Label className={hideLabel ? "sr-only" : undefined} htmlFor={searchInputID}>{copy.label}</Label>
    <form className="source-discovery-search" onSubmit={(event) => { event.preventDefault(); void search(); }}>
      <Input id={searchInputID} type="search" value={query} placeholder={copy.placeholder} onChange={(event) => setQuery(event.target.value)} />
      <Button type="submit" aria-label={session?.status === "searching" ? copy.searching : copy.search} disabled={!query.trim() || busy}><MaterialSymbol name="search" size={20} /></Button>
    </form>
    {showResults && session?.status === "searching" ? <p className="source-discovery-status" role="status">{copy.searching}</p> : null}
    {showResults && error ? <p className="source-discovery-error" role="alert">{error}</p> : null}
    {showResults && session?.status === "failed" ? <div><p className="source-discovery-error" role="alert">{copy.failed}</p><Button variant="outline" disabled={busy} onClick={() => void retrySearch()}>{copy.retry}</Button></div> : null}
    {showResults && session?.status === "ready" && visibleCandidates.length === 0 ? <p className="source-discovery-status">{copy.noResults}</p> : null}
    {showResults && hasResults && !detailOpen ? <div className="source-discovery-card">
      <div className="source-discovery-card-header">
        <strong><MaterialSymbol name="travel_explore" size={19} />{copy.researchComplete}</strong>
        <Button className="source-discovery-view" variant="link" size="sm" onClick={onViewResults}>{copy.viewResults}</Button>
      </div>
      <div className="source-discovery-previews">
        {previewCandidates.map((candidate) => <article className="source-discovery-preview" key={candidate.id}>
          <SourceSiteIcon className="source-discovery-site-icon" href={candidate.canonical_url} preferredSrc={candidate.favicon_ref} />
          <div className="source-discovery-preview-copy">
            <a href={candidate.canonical_url} target="_blank" rel="noreferrer noopener" aria-label={`${candidate.title} · ${copy.openResult}`}>{candidate.title}</a>
            <p>{candidate.snippet}</p>
          </div>
        </article>)}
      </div>
      {remainingCandidateCount > 0 ? <p className="source-discovery-more"><MaterialSymbol name="link" size={17} />{copy.moreSources.replace("{count}", String(remainingCandidateCount))}</p> : null}
      <div className="source-discovery-card-actions">
        <Button className="source-discovery-import" disabled={selectedIDs.length === 0 || busy} onClick={() => void importSelected()}><MaterialSymbol name="add" size={18} />{copy.importSelected}</Button>
      </div>
    </div> : null}
    {showResults && hasResults && detailOpen ? <div className="source-discovery-expanded">
      {session.summary ? <p className="source-discovery-summary">{session.summary}</p> : null}
      <div className="source-discovery-toolbar">
        <label>{copy.selectAll}<input aria-label={copy.selectAll} type="checkbox" checked={allSelected} onChange={() => void replaceSelection(allSelected ? [] : importable.map((candidate) => candidate.id))} /></label>
      </div>
      <div className="source-discovery-results">
        {visibleCandidates.map((candidate) => <article className="source-discovery-result" key={candidate.id}>
          <SourceSiteIcon className="source-discovery-site-icon" href={candidate.canonical_url} preferredSrc={candidate.favicon_ref} />
          <div className="source-discovery-result-copy">
            <a href={candidate.canonical_url} target="_blank" rel="noreferrer noopener" aria-label={`${candidate.title} · ${copy.openResult}`}>{candidate.title} ↗</a>
            <span>{candidate.display_url}</span>
            <p>{candidate.snippet}</p>
            {candidate.status === "imported" ? <small className="source-discovery-imported">{copy.imported}</small> : null}
          </div>
          <input
            className="source-discovery-checkbox"
            type="checkbox"
            aria-label={candidate.title}
            disabled={busy || candidate.status === "importing" || candidate.status === "imported"}
            checked={candidate.selected}
            onChange={() => void replaceSelection(candidate.selected ? selectedIDs.filter((id) => id !== candidate.id) : [...selectedIDs, candidate.id])}
          />
        </article>)}
      </div>
      <Button className="source-discovery-import" disabled={selectedIDs.length === 0 || busy} onClick={() => void importSelected()}>{copy.importSelected}</Button>
    </div> : null}
  </section>;
}
