package researchsource

import (
	"testing"

	"github.com/huangxinxinyu/nano-notebook/internal/source"
)

func TestImportResultReportsTerminalAdmissionReviewAsNotSearchable(t *testing.T) {
	result := importResult("src_review", "srcjob_review", source.StateQualifying, "succeeded", "https://example.com/paper.pdf", false, true)
	if result.State != "review_required" || result.Searchable || !result.Reused {
		t.Fatalf("result=%+v", result)
	}
}
