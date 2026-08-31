/**
 * Typed error contract mirroring the repo's Go sidecar conventions
 * (source-fetcher / document-renderer): every failure maps to a stable
 * `code` and an HTTP status, and the JSON body is always
 * `{"error":{"code":"...","message":"..."}}`.
 */

export type ErrorCode =
  | 'invalid_request'
  | 'unauthorized'
  | 'not_found'
  | 'method_not_allowed'
  | 'unsafe_destination'
  | 'response_too_large'
  | 'unsupported_type'
  | 'document_type_mismatch'
  | 'upstream_failed'
  | 'parse_failed'
  | 'engine_unavailable'
  | 'service_busy'
  | 'internal_error';

const STATUS_BY_CODE: Record<ErrorCode, number> = {
  invalid_request: 400,
  unauthorized: 401,
  not_found: 404,
  method_not_allowed: 405,
  unsafe_destination: 422,
  response_too_large: 413,
  unsupported_type: 415,
  document_type_mismatch: 415,
  upstream_failed: 502,
  parse_failed: 422,
  engine_unavailable: 503,
  service_busy: 503,
  internal_error: 500,
};

export class ReaderError extends Error {
  readonly code: ErrorCode;
  readonly status: number;

  constructor(code: ErrorCode, message: string) {
    super(message);
    this.name = 'ReaderError';
    this.code = code;
    this.status = STATUS_BY_CODE[code];
  }
}

export function errorBody(code: ErrorCode, message: string): string {
  return JSON.stringify({ error: { code, message } });
}
