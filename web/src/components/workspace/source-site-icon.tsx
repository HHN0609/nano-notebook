import { useMemo, useState } from "react";
import { MaterialSymbol } from "../icons/material-symbol";

function sourceFaviconURL(rawURL: string) {
  try {
    const parsed = new URL(rawURL);
    if (parsed.protocol !== "http:" && parsed.protocol !== "https:") return undefined;
    return new URL("/favicon.ico", parsed.origin).toString();
  } catch {
    return undefined;
  }
}

export function SourceSiteIcon({ href, preferredSrc, className = "" }: {
  href: string;
  preferredSrc?: string;
  className?: string;
}) {
  const src = useMemo(() => preferredSrc || sourceFaviconURL(href), [href, preferredSrc]);
  const [failedSrc, setFailedSrc] = useState<string>();

  return <span className={`source-site-icon${className ? ` ${className}` : ""}`} aria-hidden="true">
    {src && failedSrc !== src ? <img src={src} alt="" onError={() => setFailedSrc(src)} /> : <MaterialSymbol name="language" size={18} />}
  </span>;
}
