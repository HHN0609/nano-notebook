import {
  AssistantRuntimeProvider,
  ComposerPrimitive,
  MessagePrimitive,
  ThreadPrimitive,
  useAuiState,
  useExternalStoreRuntime,
  type AssistantRuntime
} from "@assistant-ui/react";
import { MarkdownTextPrimitive } from "@assistant-ui/react-markdown";
import { useCallback, useEffect, useMemo, useRef, type ComponentProps } from "react";
import remarkGfm from "remark-gfm";
import { MaterialSymbol } from "../icons/material-symbol";
import { Button } from "../ui/button";
import { appendMessageText, type ChatController, type ChatMessage, type Citation } from "./private-chat";
import { SourceOpenTarget } from "./source-open-target";
import type { MemberSource } from "./sources";

export type ChatPanelCopy = {
  title: string;
  emptyTitle: string;
  emptyBody: string;
  composerPlaceholder: string;
  composerLabel: string;
  sendLabel: string;
  waitingLabel: string;
  generatingLabel: string;
  sourceDisclosure: string;
  selectedSourceDisclosure: string;
  failedLabel: string;
  stoppedLabel: string;
  stopLabel: string;
  retryLabel: string;
  unavailableLabel: string;
  citationLabel: string;
  citationUnavailableLabel: string;
  citationPreviewLabel: string;
  closeLabel: string;
  sourcePreviewLabel: string;
  processingLabel: string;
  sourceUnavailableLabel: string;
  coverageWarningLabel: string;
};

export function ChatPanelContent({ copy, controller, sources, onOpenSource, selectedSourceCount = 0 }: { copy: ChatPanelCopy; controller: ChatController; sources: MemberSource[]; onOpenSource: (source: MemberSource) => void; selectedSourceCount?: number }) {
  const messages = controller.snapshot?.messages ?? [];
  const runs = controller.snapshot?.runs ?? [];
  const run = runs.find((item) => item.status === "queued" || item.status === "running");
  const isRunning = run?.status === "queued" || run?.status === "running";
  const latestMessageID = messages.at(-1)?.id;
  const runtimeRef = useRef<AssistantRuntime | null>(null);
  const runtime = useExternalStoreRuntime<ChatMessage>({
    messages,
    isLoading: controller.isLoading,
    isDisabled: !controller.snapshot,
    isSendDisabled: isRunning,
    isRunning,
    onNew: async (message) => {
      if (!await controller.send(message)) {
        runtimeRef.current?.thread.composer.setText(appendMessageText(message));
      }
    },
    convertMessage: (message) => ({
      id: message.id,
      role: message.role,
      content: message.content,
      createdAt: new Date(message.created_at),
      ...(message.role === "assistant" ? { status: { type: "complete" as const, reason: "stop" as const } } : {})
    })
  });
  useEffect(() => {
    runtimeRef.current = runtime;
    return () => {
      runtimeRef.current = null;
    };
  }, [runtime]);

  return (
    <AssistantRuntimeProvider runtime={runtime}>
      <div className="workspace-panel-content chat-content" data-chat-framework="@assistant-ui/react">
        <div className="workspace-panel-header">
          <h2>{copy.title}</h2>
          <MaterialSymbol name="more_vert" size={20} />
        </div>
        <p className="chat-source-disclosure">{selectedSourceCount > 0 ? copy.selectedSourceDisclosure.replace("{count}", String(selectedSourceCount)) : copy.sourceDisclosure}</p>
        <ThreadPrimitive.Root className="chat-thread">
          <ThreadPrimitive.Viewport className="chat-thread-viewport">
            <ThreadPrimitive.Empty>
              <div className="chat-empty-state">
                <span className="chat-empty-icon"><MaterialSymbol name="chat_bubble" size={27} /></span>
                <strong>{copy.emptyTitle}</strong>
                <p>{copy.emptyBody}</p>
              </div>
            </ThreadPrimitive.Empty>
            <div className="chat-message-list">
              <ThreadPrimitive.Messages components={{
                UserMessage: () => <UserMessage controller={controller} copy={copy} latestMessageID={latestMessageID} />,
                AssistantMessage: () => <AssistantMessage controller={controller} copy={copy} sources={sources} onOpenSource={onOpenSource} />
              }} />
            </div>
            {run ? (
              <div className="chat-activity" role="status">
                <span>{run.status === "queued" ? copy.waitingLabel : copy.generatingLabel}</span>
                <Button variant="ghost" size="sm" onClick={() => void controller.stop(run.id)}>{copy.stopLabel}</Button>
              </div>
            ) : null}
            {controller.error ? <div className="chat-error" role="alert">{controller.error}</div> : null}
          </ThreadPrimitive.Viewport>
          <ComposerPrimitive.Root className="chat-composer">
            <ComposerPrimitive.Input className="chat-composer-input" aria-label={copy.composerLabel} placeholder={copy.composerPlaceholder} rows={1} />
            <ComposerPrimitive.Send className="chat-send" aria-label={copy.sendLabel}>
              <MaterialSymbol name="arrow_upward" size={22} />
            </ComposerPrimitive.Send>
          </ComposerPrimitive.Root>
        </ThreadPrimitive.Root>
      </div>
    </AssistantRuntimeProvider>
  );
}

function UserMessage({ controller, copy, latestMessageID }: { controller: ChatController; copy: ChatPanelCopy; latestMessageID?: string }) {
  const messageID = useAuiState((state) => state.message.id);
  const run = controller.snapshot?.runs.find((item) => item.input_message_id === messageID);
  const canRetry = messageID === latestMessageID && (run?.status === "failed" || run?.status === "cancelled");
  return (
    <MessagePrimitive.Root className="chat-message chat-message--user">
      <MessagePrimitive.Parts />
      {run?.status === "failed" || run?.status === "cancelled" ? (
        <span className="chat-run-terminal">
          {run.status === "failed" ? copy.failedLabel : copy.stoppedLabel}
          {canRetry ? <Button variant="ghost" size="sm" onClick={() => void controller.retry(run.id)}>{copy.retryLabel}</Button> : null}
        </span>
      ) : null}
    </MessagePrimitive.Root>
  );
}

function AssistantMessage({ controller, copy, sources, onOpenSource }: { controller: ChatController; copy: ChatPanelCopy; sources: MemberSource[]; onOpenSource: (source: MemberSource) => void }) {
  const messageID = useAuiState((state) => state.message.id);
  const citations = controller.snapshot?.citations.filter((citation) => citation.message_id === messageID) ?? [];
  const sourceCitations = citations
    .filter((citation) => citation.reference_kind === "source")
    .sort((left, right) => (left.reference_ordinal ?? 0) - (right.reference_ordinal ?? 0));
  const preciseCitations = citations.filter((citation) => citation.reference_kind !== "source");
  return (
    <MessagePrimitive.Root className="chat-message chat-message--assistant">
      <MessagePrimitive.Parts>
        {({ part }) => part.type === "text" ? <AssistantMarkdownText citations={sourceCitations} copy={copy} sources={sources} onOpenSource={onOpenSource} /> : null}
      </MessagePrimitive.Parts>
      {preciseCitations.length ? <div className="chat-citations">{preciseCitations.map((citation, index) => <CitationButton key={citation.id} citation={citation} number={index + 1} copy={copy} source={sources.find((item) => item.id === citation.source_id)} onOpenSource={onOpenSource} />)}</div> : null}
    </MessagePrimitive.Root>
  );
}

function AssistantMarkdownText({ citations, copy, sources, onOpenSource }: { citations: Citation[]; copy: ChatPanelCopy; sources: MemberSource[]; onOpenSource: (source: MemberSource) => void }) {
  const bySource = useMemo(() => new Map(citations.map((citation) => [citation.source_id, citation])), [citations]);
  const citationTargets = useMemo(() => new Map(citations.map((citation) => [sourceCitationTarget(citation.source_id), citation])), [citations]);
  const preprocess = useCallback((text: string) => {
    return text.replace(/\[source:([A-Za-z0-9_.-]{1,255})\]/g, (marker, sourceID: string) => {
      const citation = bySource.get(sourceID);
      if (!citation) return marker;
      return `[${(citation.reference_ordinal ?? 0) + 1}](${sourceCitationTarget(sourceID)})`;
    });
  }, [bySource]);
  const components = useMemo<NonNullable<ComponentProps<typeof MarkdownTextPrimitive>["components"]>>(() => ({
    a: ({ href, children, title, className }) => {
      const citation = href ? citationTargets.get(href) : undefined;
      if (citation) return <InlineSourceCitationButton citation={citation} copy={copy} source={sources.find((item) => item.id === citation.source_id)} onOpenSource={onOpenSource} />;
      const external = href?.startsWith("https://") || href?.startsWith("http://");
      return <a href={href} title={title} className={className} {...(external ? { target: "_blank", rel: "noopener noreferrer" } : {})}>{children}</a>;
    }
  }), [citationTargets, copy, onOpenSource, sources]);
  return (
    <MarkdownTextPrimitive
      className="chat-markdown"
      components={components}
      preprocess={preprocess}
      remarkPlugins={[remarkGfm]}
      smooth={false}
    />
  );
}

function sourceCitationTarget(sourceID: string) {
  return `#source-citation-${encodeURIComponent(sourceID)}`;
}

function InlineSourceCitationButton({ citation, copy, source, onOpenSource }: { citation: Citation; copy: ChatPanelCopy; source?: MemberSource; onOpenSource: (source: MemberSource) => void }) {
  const number = (citation.reference_ordinal ?? 0) + 1;
  const sourceLabel = source?.title ?? citation.source_title ?? `${copy.citationLabel} ${number}`;
  return (
    <SourceOpenTarget source={source} className="nn-button nn-button--outline nn-button--size-sm citation-chip citation-chip--inline" ariaLabel={`${copy.citationLabel} ${number} for ${sourceLabel}`} onInlineOriginal={onOpenSource}>[{number}] {sourceLabel}</SourceOpenTarget>
  );
}

function CitationButton({ citation, number, copy, source, onOpenSource }: { citation: Citation; number: number; copy: ChatPanelCopy; source?: MemberSource; onOpenSource: (source: MemberSource) => void }) {
  const sourceLabel = source?.title ?? citation.source_title ?? `${copy.citationLabel} ${number}`;
  const accessibleContext = citation.claim_text ?? sourceLabel;
  return (
    <SourceOpenTarget source={source} className="nn-button nn-button--outline nn-button--size-sm citation-chip" ariaLabel={`${copy.citationLabel} ${number} for ${accessibleContext}`} onInlineOriginal={onOpenSource}>[{number}] {sourceLabel}</SourceOpenTarget>
  );
}
