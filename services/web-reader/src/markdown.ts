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

/**
 * Markdown links with URLs beyond this length (entity auto-links, tracking
 * parameters, signed CDN hrefs) are flattened to their label text: the hrefs
 * are pure noise for LLM consumption.
 */
const MAX_LINK_URL_CHARS = 100;

/** Blocks at or below this collapsed length are fragment candidates. */
const FRAGMENT_MAX_CHARS = 60;
/** Runs of this many consecutive fragments become a compact markdown list. */
const FRAGMENT_MIN_RUN = 3;

/** Lines made only of interpunct-style separators (figure/card chrome). */
const DECORATIVE_LINE = /^[\s·•‧∙]+$/u;

/** Block-level markdown markers; blocks starting with them keep their shape. */
const STRUCTURED_BLOCK = /^(?:#{1,6}\s|[-*+]\s|\d+[.)]\s|>\s|```|\||!)/;

/** Sentence-final punctuation: real prose paragraphs end with it, figure and
 * card fragments (labels, timelines, stats) almost never do. */
const SENTENCE_FINAL = /[。．.!?!?…；;：:]$/;
/** Trailing closers stripped before the sentence-final check. */
const TRAILING_CLOSERS = /[」』""''）)\]】》>*_~]+$/;

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
      // Oversized hrefs (entity auto-links, tracking parameters) carry no
      // signal for LLM consumption; keep the label, drop the url.
      if (href.length > MAX_LINK_URL_CHARS) {
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

  // Regroup fragmented figure/card text into compact markdown lists.
  normalized = regroupFragments(normalized);

  // Trim trailing whitespace on each line.
  normalized = normalized.replace(/[ \t]+$/gm, '');

  // Revert benign turndown escapes that only add LLM noise.
  normalized = normalized.replace(/\\([_\[\]])/g, '$1');

  return normalized.trim();
}

/**
 * Figures, timelines and card grids on modern platforms (zhihu, wechat
 * articles, …) are laid out as sequences of tiny sibling <p> blocks without
 * any structural markup, which turndown renders as many one-line paragraphs
 * separated by blank lines. Runs of consecutive short plain blocks are
 * regrouped into a single compact markdown list, and decorative
 * separator-only lines (figure chrome) are dropped.
 */
function regroupFragments(markdown: string): string {
  const blocks = splitBlocks(markdown);

  const output: string[] = [];
  let run: string[] = [];

  const flushRun = () => {
    if (run.length === 0) return;
    if (run.length >= FRAGMENT_MIN_RUN) {
      output.push(
        run
          .map((fragment) => `- ${fragment.replace(/\n/g, ' ').replace(/\s+/g, ' ').trim()}`)
          .join('\n'),
      );
    } else {
      output.push(...run);
    }
    run = [];
  };

  for (const block of blocks) {
    const collapsed = block.replace(/\n/g, ' ').replace(/\s+/g, ' ').trim();
    if (collapsed === '') continue;
    if (DECORATIVE_LINE.test(collapsed)) continue; // figure chrome, drop it
    if (
      collapsed.length <= FRAGMENT_MAX_CHARS &&
      !STRUCTURED_BLOCK.test(collapsed) &&
      !endsLikeSentence(collapsed) &&
      !block.includes('```')
    ) {
      run.push(block);
      continue;
    }
    flushRun();
    output.push(block);
  }
  flushRun();

  return output.join('\n\n');
}

/** True when the block reads like a finished sentence (prose), not a fragment. */
function endsLikeSentence(collapsed: string): boolean {
  const stripped = collapsed.replace(TRAILING_CLOSERS, '').trimEnd();
  return SENTENCE_FINAL.test(stripped);
}

/** Split on blank lines, keeping fenced code blocks intact. */
function splitBlocks(markdown: string): string[] {
  const blocks: string[] = [];
  let current: string[] = [];
  let inFence = false;

  for (const line of markdown.split('\n')) {
    if (/^\s*```/.test(line)) {
      inFence = !inFence;
      current.push(line);
      continue;
    }
    if (!inFence && line.trim() === '') {
      if (current.length > 0) {
        blocks.push(current.join('\n'));
        current = [];
      }
      continue;
    }
    current.push(line);
  }
  if (current.length > 0) {
    blocks.push(current.join('\n'));
  }
  return blocks;
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
