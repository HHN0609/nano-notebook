/**
 * HTML → Markdown conversion following the jina-ai/reader approach:
 * a rule-based engine (turndown + GFM) with link/image normalization,
 * followed by the `tidyMarkdown` post-processing step ported from
 * jina-ai/reader (src/utils/markdown.ts).
 */

import TurndownService from 'turndown';
import { gfm } from 'turndown-plugin-gfm';
import { JSDOM } from 'jsdom';

export interface MarkdownOptions {
  baseUrl: string;
  withLinks: boolean;
  withImages: boolean;
}

const TRACKER_SIZE_LIMIT = 2;

export function htmlToMarkdown(html: string, options: MarkdownOptions): string {
  const dom = new JSDOM(html, { url: options.baseUrl });
  const body = dom.window.document.body;
  if (!body) return '';

  const service = new TurndownService({
    headingStyle: 'atx',
    codeBlockStyle: 'fenced',
    bulletListMarker: '-',
    emDelimiter: '*',
    strongDelimiter: '**',
    hr: '---',
  });
  service.use(gfm);

  // Turndown hardcodes `marker + '   '` for list items; emit compact
  // CommonMark-style markers (`- item`, `1. item`) instead.
  service.addRule('nanoListItems', {
    filter: 'li',
    replacement: (content, node) => {
      const normalized = content.replace(/^\n+/, '').replace(/\n+$/, '\n').replace(/\n/gm, '\n  ');
      let prefix = '- ';
      const parent = node.parentNode as HTMLElement | null;
      if (parent && parent.nodeName === 'OL') {
        const start = Number.parseInt(parent.getAttribute('start') ?? '1', 10);
        const index = Array.prototype.indexOf.call(parent.children, node) + 1;
        prefix = `${Number.isNaN(start) ? index : index + start - 1}. `;
      }
      const hasSibling = node.nextSibling !== null && !/\n$/.test(normalized);
      return prefix + normalized + (hasSibling ? '\n' : '');
    },
  });

  service.addRule('nanoLinks', {
    filter: (node) => node.nodeName === 'A' && node.getAttribute('href') !== null,
    replacement: (content, node) => {
      if (!options.withLinks) {
        return collapseWhitespace(content);
      }
      const text = collapseWhitespace(content).trim();
      if (text === '') {
        return '';
      }
      const href = absoluteUrl(node.getAttribute('href') ?? '', options.baseUrl);
      if (!href) {
        return text;
      }
      return `[${text}](${href})`;
    },
  });

  service.addRule('nanoImages', {
    filter: 'img',
    replacement: (_content, node) => {
      if (!options.withImages) {
        return '';
      }
      const rawSrc = node.getAttribute('src') ?? node.getAttribute('data-src') ?? '';
      if (rawSrc === '' || /^\s*data:/i.test(rawSrc)) {
        return '';
      }
      if (isTrackerSized(node)) {
        return '';
      }
      const src = absoluteUrl(rawSrc, options.baseUrl);
      if (!src) {
        return '';
      }
      const alt = collapseWhitespace(node.getAttribute('alt') ?? '').trim();
      return `![${alt}](${src})`;
    },
  });

  const markdown = service.turndown(body);
  return tidyMarkdown(markdown);
}

/**
 * Markdown post-processing ported from jina-ai/reader `tidyMarkdown`, with
 * two deliberate deviations: leading whitespace on lines is preserved
 * (the upstream regex strips indentation inside fenced code blocks), and
 * benign turndown escapes (`\_`, `\[`, `\]`) are reverted because they are
 * pure noise for LLM consumption.
 */
export function tidyMarkdown(markdown: string): string {
  let normalized = markdown.replace(/\r\n?/g, '\n');

  // Normalize links whose text or URL is padded with whitespace (possibly
  // across line breaks inside the brackets). The `](` pair must stay on one
  // line so unrelated bracketed text is never rewritten.
  normalized = normalized.replace(
    /\[([^\]]+?)\]\([ \t]*([^)\n]+?)[ \t]*\)/g,
    (_match, text: string, url: string) => {
      return `[${text.replace(/\s+/g, ' ').trim()}](${url.replace(/\s+/g, '').trim()})`;
    },
  );

  // Collapse more than two consecutive empty lines into exactly one blank line.
  normalized = normalized.replace(/\n{3,}/g, '\n\n');

  // Trim trailing whitespace on each line.
  normalized = normalized.replace(/[ \t]+$/gm, '');

  // Revert benign turndown escapes that only add LLM noise.
  normalized = normalized.replace(/\\([_\[\]])/g, '$1');

  return normalized.trim();
}

function collapseWhitespace(text: string): string {
  return text.replace(/\s+/g, ' ');
}

function absoluteUrl(rawHref: string, baseUrl: string): string | null {
  const href = rawHref.trim();
  if (href === '') {
    return null;
  }
  if (/^(javascript|vbscript|data|about|blob)\s*:/i.test(href)) {
    return null;
  }
  try {
    const url = new URL(href, baseUrl);
    if (url.protocol !== 'http:' && url.protocol !== 'https:' && url.protocol !== 'mailto:') {
      return null;
    }
    return url.toString();
  } catch {
    return null;
  }
}

function isTrackerSized(node: Element): boolean {
  const width = parseSize(node.getAttribute('width'));
  const height = parseSize(node.getAttribute('height'));
  if (width !== null && width <= TRACKER_SIZE_LIMIT) return true;
  if (height !== null && height <= TRACKER_SIZE_LIMIT) return true;
  return false;
}

function parseSize(raw: string | null): number | null {
  if (raw === null) return null;
  const value = Number.parseInt(raw, 10);
  return Number.isNaN(value) ? null : value;
}
