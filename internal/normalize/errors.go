package normalize

import "errors"

var ErrProcessingBudget = errors.New("normalized Source processing budget exceeded")

// ErrHTMLQuality identifies a deterministic hard-quality rejection. Callers
// may safely map it to unreadable content without exposing fetched page text.
var ErrHTMLQuality = errors.New("HTML Source failed deterministic quality gate")
