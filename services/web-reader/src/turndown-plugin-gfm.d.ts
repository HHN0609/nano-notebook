/**
 * Type shim for the untyped `turndown-plugin-gfm` package
 * (GFM tables / strikethrough / task lists for turndown).
 */

declare module 'turndown-plugin-gfm' {
  import type TurndownService from 'turndown';

  type Plugin = (service: TurndownService) => void;

  export const gfm: Plugin;
  export const tables: Plugin;
  export const strikethrough: Plugin;
  export const taskListItems: Plugin;
}
