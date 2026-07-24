import { useEffect, useMemo, useState } from "react";
import { MaterialSymbol } from "../icons/material-symbol";
import { Button } from "../ui/button";
import { Input } from "../ui/input";
import { Label } from "../ui/label";
import { csrfToken, memberAPI } from "./source-upload";

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
};

export function SourceDiscovery({ notebookID, originChatID, requestedSessionID, active, copy, onExpandedChange, onImported }: {
  notebookID: string;
  originChatID?: string;
  requestedSessionID?: string;
  active: boolean;
  copy: SourceDiscoveryCopy;
  onExpandedChange: (expanded: boolean) => void;
  onImported: () => void | Promise<unknown>;
}) {
  const [query, setQuery] = useState("");
  const [session, setSession] = useState<DiscoverySession | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const expanded = session?.status === "ready" && session.candidates.length > 0;

  useEffect(() => onExpandedChange(Boolean(expanded)), [expanded, onExpandedChange]);
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
      }
    });
    return () => { cancelled = true; };
  }, [active, notebookID, onExpandedChange, requestedSessionID]);

  useEffect(() => {
    if (!active || session?.status !== "searching") return;
    const timer = window.setTimeout(async () => {
      const response = await memberAPI(`/api/v1/source-discovery-sessions/${session.id}`);
      if (response.ok) setSession(((await response.json()) as { session: DiscoverySession }).session);
    }, 1000);
    return () => window.clearTimeout(timer);
  }, [active, session]);

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

  const importable = useMemo(() => session?.candidates.filter((candidate) => candidate.status === "discovered" || candidate.status === "import_failed") ?? [], [session]);
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
      const refreshed = await memberAPI(`/api/v1/source-discovery-sessions/${session.id}`);
      if (refreshed.ok) setSession(((await refreshed.json()) as { session: DiscoverySession }).session);
      await onImported();
    } catch {
      setError(copy.importFailed);
    } finally {
      setBusy(false);
    }
  }

  return <section className="source-discovery">
    <Label htmlFor="source-web-search">{copy.label}</Label>
    <form className="source-discovery-search" onSubmit={(event) => { event.preventDefault(); void search(); }}>
      <Input id="source-web-search" type="search" value={query} placeholder={copy.placeholder} onChange={(event) => setQuery(event.target.value)} />
      <Button type="submit" disabled={!query.trim() || busy}>{session?.status === "searching" ? copy.searching : copy.search}</Button>
    </form>
    {session?.status === "searching" ? <p className="source-discovery-status" role="status">{copy.searching}</p> : null}
    {error ? <p className="source-discovery-error" role="alert">{error}</p> : null}
    {session?.status === "failed" ? <div><p className="source-discovery-error" role="alert">{copy.failed}</p><Button variant="outline" disabled={busy} onClick={() => void retrySearch()}>{copy.retry}</Button></div> : null}
    {session?.status === "ready" && session.candidates.length === 0 ? <p className="source-discovery-status">{copy.noResults}</p> : null}
    {expanded ? <div className="source-discovery-expanded">
      {session.summary ? <p className="source-discovery-summary">{session.summary}</p> : null}
      <div className="source-discovery-toolbar">
        <label>{copy.selectAll}<input aria-label={copy.selectAll} type="checkbox" checked={allSelected} onChange={() => void replaceSelection(allSelected ? [] : importable.map((candidate) => candidate.id))} /></label>
      </div>
      <div className="source-discovery-results">
        {session.candidates.map((candidate) => <article className="source-discovery-result" key={candidate.id}>
          <span className="source-discovery-site-icon" aria-hidden="true">
            {candidate.favicon_ref ? <img src={candidate.favicon_ref} alt="" /> : <MaterialSymbol name="language" size={18} />}
          </span>
          <div className="source-discovery-result-copy">
            <a href={candidate.canonical_url} target="_blank" rel="noreferrer noopener" aria-label={`${candidate.title} · ${copy.openResult}`}>{candidate.title} ↗</a>
            <span>{candidate.display_url}</span>
            <p>{candidate.snippet}</p>
            {candidate.status === "import_failed" ? <small>{copy.importFailed}</small> : null}
            {candidate.status === "imported" ? <small>{copy.imported}</small> : null}
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
