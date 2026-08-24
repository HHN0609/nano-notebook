/**
 * Page parsing pipeline following the jina-ai/reader default profile:
 *   1. Parse the HTML with jsdom (no script execution, no resource loading).
 *   2. Pre-clean the DOM (unwanted tags, hidden elements, noise containers).
 *   3. Extract the main content with Mozilla Readability.
 *   4. Fall back to the pre-cleaned <body> when Readability yields nothing.
 *   5. Render the content as Markdown / plain text / cleaned HTML.
 */

import { JSDOM } from 'jsdom';
import { Readability } from '@mozilla/readability';

import { htmlToMarkdown } from './markdown.js';
import { ReaderError } from './errors.js';

export type OutputFormat = 'markdown' | 'text' | 'html';
export type ExtractionMethod = 'readability' | 'fallback-body';

export interface ParseOptions {
  format: OutputFormat;
  withLinks: boolean;
  withImages: boolean;
}

export interface ParseResult {
  title: string;
  description: string;
  siteName: string;
  publishedTime: string | null;
  lang: string;
  extraction: ExtractionMethod;
  content: string;
  wordCount: number;
}

export const OUTPUT_FORMATS: readonly OutputFormat[] = ['markdown', 'text', 'html'];

/** Mirrors the repo's Go normalize quality gate (60 runes minimum). */
const MIN_CONTENT_CHARS = 60;

const REMOVABLE_TAGS = [
  'script',
  'style',
  'noscript',
  'template',
  'svg',
  'canvas',
  'iframe',
  'object',
  'embed',
  'form',
  'button',
  'input',
  'select',
  'textarea',
  'option',
  'dialog',
  'nav',
  'aside',
  'footer',
  'link',
  'meta',
  'base',
];

/** Class/id tokens that mark a container as chrome rather than content. */
const NOISE_CLASS_TOKENS = new Set([
  'advert',
  'ads',
  'banner',
  'breadcrumb',
  'cookie',
  'footer',
  'modal',
  'newsletter',
  'promo',
  'related',
  'share',
  'sidebar',
  'social',
  'sponsor',
  'subscribe',
  'popup',
  'offscreen',
  'noprint',
]);

/** ARIA landmark roles treated as page chrome when set via `role`. */
const NOISE_ROLE_TOKENS = new Set([
  'navigation',
  'banner',
  'contentinfo',
  'complementary',
  'menu',
  'menubar',
  'search',
]);

const NOISE_CLASS_TARGETS = new Set(['DIV', 'SECTION', 'ASIDE', 'SPAN', 'UL', 'OL']);

const BLOCK_TEXT_TAGS = new Set([
  'ADDRESS', 'ARTICLE', 'ASIDE', 'BLOCKQUOTE', 'DETAILS', 'DD', 'DIV', 'DL', 'DT',
  'FIELDSET', 'FIGCAPTION', 'FIGURE', 'FOOTER', 'FORM', 'H1', 'H2', 'H3', 'H4', 'H5',
  'H6', 'HEADER', 'HR', 'LI', 'MAIN', 'NAV', 'OL', 'P', 'PRE', 'SECTION', 'SUMMARY',
  'TABLE', 'TBODY', 'TD', 'TFOOT', 'TH', 'THEAD', 'TR', 'UL',
]);

export function parsePage(html: string, baseUrl: string, options: ParseOptions): ParseResult {
  const dom = new JSDOM(html, { url: baseUrl, pretendToBeVisual: false });
  const doc = dom.window.document;

  const meta = collectMetadata(doc, baseUrl);

  preClean(doc);

  const cleanedHtml = doc.body ? doc.body.innerHTML : '';

  let articleTitle = '';
  let articleContent: string | null = null;
  try {
    const article = new Readability(doc).parse();
    if (article) {
      articleTitle = article.title?.trim() ?? '';
      if (article.content) {
        articleContent = article.content;
      }
    }
  } catch {
    // Readability rejects the document; use the fallback path below.
  }

  let extraction: ExtractionMethod = 'readability';
  let contentHtml = articleContent ?? '';
  let plainText = extractText(contentHtml);

  if (collapseWhitespace(plainText).length < MIN_CONTENT_CHARS) {
    extraction = 'fallback-body';
    contentHtml = cleanedHtml;
    plainText = extractText(contentHtml);
  }

  if (collapseWhitespace(plainText).length < MIN_CONTENT_CHARS) {
    throw new ReaderError('parse_failed', 'page has no extractable main content');
  }

  let content: string;
  switch (options.format) {
    case 'html':
      content = contentHtml.trim();
      break;
    case 'text':
      content = tidyText(plainText);
      break;
    default:
      content = htmlToMarkdown(contentHtml, {
        baseUrl,
        withLinks: options.withLinks,
        withImages: options.withImages,
      });
      break;
  }

  return {
    title: articleTitle || meta.title,
    description: meta.description,
    siteName: meta.siteName,
    publishedTime: meta.publishedTime,
    lang: meta.lang,
    extraction,
    content,
    wordCount: countWords(plainText),
  };
}

interface PageMetadata {
  title: string;
  description: string;
  siteName: string;
  publishedTime: string | null;
  lang: string;
}

function collectMetadata(doc: Document, baseUrl: string): PageMetadata {
  const title = textOf(doc.querySelector('title')) || metaContent(doc, 'meta[property="og:title"]');

  const description =
    metaContent(doc, 'meta[name="description"]') || metaContent(doc, 'meta[property="og:description"]');

  let siteName =
    metaContent(doc, 'meta[property="og:site_name"]') || metaContent(doc, 'meta[name="application-name"]');
  if (siteName === '') {
    try {
      siteName = new URL(baseUrl).hostname;
    } catch {
      siteName = '';
    }
  }

  const publishedTime =
    metaContent(doc, 'meta[property="article:published_time"]') ||
    metaContent(doc, 'meta[name="date"]') ||
    null;

  const lang = doc.documentElement.getAttribute('lang')?.trim() ?? '';

  return { title, description, siteName, publishedTime, lang };
}

function metaContent(doc: Document, selector: string): string {
  const value = doc.querySelector(selector)?.getAttribute('content');
  return value ? value.trim() : '';
}

function textOf(node: Element | null): string {
  return node?.textContent?.trim() ?? '';
}

function preClean(doc: Document): void {
  for (const tag of REMOVABLE_TAGS) {
    for (const el of Array.from(doc.querySelectorAll(tag))) {
      el.remove();
    }
  }

  // Embedded media players: the control UI (play buttons, timestamps,
  // quality menus, float-window tooltips) is interaction chrome that
  // Readability would otherwise pull into the article text. Match the
  // "player" substring in class/id across both common casings, plus
  // loading spinners and leftover media elements.
  for (const el of Array.from(
    doc.querySelectorAll(
      '[class*="player"], [class*="Player"], [id*="player"], [id*="Player"], [class*="loading"], [class*="Loading"]',
    ),
  )) {
    el.remove();
  }

  for (const el of Array.from(doc.querySelectorAll('video, audio, source, track'))) {
    el.remove();
  }

  for (const el of Array.from(doc.querySelectorAll('[hidden], [aria-hidden="true"]'))) {
    el.remove();
  }

  for (const el of Array.from(doc.querySelectorAll('[style]'))) {
    const style = el.getAttribute('style') ?? '';
    if (/display\s*:\s*none|visibility\s*:\s*hidden|opacity\s*:\s*0(?![.\d])/i.test(style)) {
      el.remove();
    }
  }

  // Site chrome headers stay only when they belong to the article itself.
  for (const el of Array.from(doc.querySelectorAll('header'))) {
    if (!el.closest('article, main, [role="main"]')) {
      el.remove();
    }
  }

  for (const el of Array.from(doc.body?.querySelectorAll('*') ?? [])) {
    if (!NOISE_CLASS_TARGETS.has(el.tagName)) continue;
    if (hasNoiseClass(el) || hasNoiseRole(el)) {
      el.remove();
    }
  }
}

function hasNoiseClass(el: Element): boolean {
  const raw = `${el.getAttribute('class') ?? ''} ${el.id ?? ''}`;
  if (raw.trim() === '') return false;
  for (const token of raw.toLowerCase().split(/[^a-z0-9]+/)) {
    if (token !== '' && NOISE_CLASS_TOKENS.has(token)) return true;
  }
  return false;
}

function hasNoiseRole(el: Element): boolean {
  const role = (el.getAttribute('role') ?? '').trim().toLowerCase();
  return role !== '' && NOISE_ROLE_TOKENS.has(role);
}

function extractText(html: string): string {
  if (html.trim() === '') return '';
  const dom = new JSDOM(html);
  const body = dom.window.document.body;
  if (!body) return '';
  const parts: string[] = [];
  collectBlockText(body, parts);
  return parts.join('');
}

function collectBlockText(node: Node, out: string[]): void {
  if (node.nodeType === 3) {
    out.push(node.nodeValue ?? '');
    return;
  }
  if (node.nodeType !== 1) {
    return;
  }
  const el = node as Element;
  if (el.tagName === 'BR') {
    out.push('\n');
    return;
  }
  const isBlock = BLOCK_TEXT_TAGS.has(el.tagName);
  if (isBlock) {
    out.push('\n');
  }
  for (const child of Array.from(el.childNodes)) {
    collectBlockText(child, out);
  }
  if (isBlock) {
    out.push('\n');
  }
}

function tidyText(text: string): string {
  return text
    .split('\n')
    .map((line) => collapseWhitespace(line).trim())
    .join('\n')
    .replace(/\n{3,}/g, '\n\n')
    .trim();
}

function collapseWhitespace(text: string): string {
  return text.replace(/\s+/g, ' ').trim();
}

function countWords(text: string): number {
  const collapsed = collapseWhitespace(text);
  if (collapsed === '') return 0;
  return collapsed.split(' ').length;
}
