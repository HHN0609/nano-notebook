import type { ReactNode } from "react";
import type { MemberSource } from "./sources";

export function SourceOpenTarget({ source, className, ariaLabel, onInlineOriginal, children }: {
  source: MemberSource | undefined;
  className: string;
  ariaLabel?: string;
  onInlineOriginal: (source: MemberSource) => void;
  children: ReactNode;
}) {
  const action = source?.open_action;
  if (source && action?.kind === "external" && action.href) {
    return <a className={className} href={action.href} target="_blank" rel="noreferrer noopener" aria-label={ariaLabel}>{children}</a>;
  }
  if (source && action?.kind === "inline_original" && action.href && action.media_type) {
    return <button className={className} type="button" aria-label={ariaLabel} onClick={() => onInlineOriginal(source)}>{children}</button>;
  }
  return <span className={className} aria-label={ariaLabel}>{children}</span>;
}
