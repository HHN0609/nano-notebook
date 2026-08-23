package sourceadmission

import (
	"math"
	"testing"

	"github.com/huangxinxinyu/nano-notebook/internal/normalize"
)

func TestObserveExtractionDerivesQualityFromNormalizedArtifact(t *testing.T) {
	artifact, err := normalize.Text(normalize.Input{
		SourceID: "src_quality", ExtractionConfigID: "extract-v1", Format: "txt",
		Payload: []byte("Repeated block\n\nRepeated block\n\nUnique replacement \ufffd block"),
	})
	if err != nil {
		t.Fatalf("normalize.Text: %v", err)
	}

	observation, err := ObserveExtraction(artifact)
	if err != nil {
		t.Fatalf("ObserveExtraction: %v", err)
	}
	if observation.CoverageStatus != "complete" || observation.TotalRunes != artifact.Coverage.TotalRunes ||
		observation.BlockCount != 3 || observation.GapCount != 0 {
		t.Fatalf("observation=%+v", observation)
	}
	if math.Abs(observation.RepeatedBlockRatio-(1.0/3.0)) > 0.000001 {
		t.Fatalf("repeated ratio=%v want=%v", observation.RepeatedBlockRatio, 1.0/3.0)
	}
	if observation.InvalidCharacterRatio <= 0 {
		t.Fatalf("invalid character ratio=%v want positive", observation.InvalidCharacterRatio)
	}
}

func TestObserveExtractionRejectsInvalidArtifact(t *testing.T) {
	if _, err := ObserveExtraction(normalize.Artifact{}); err == nil {
		t.Fatal("ObserveExtraction accepted an invalid Artifact")
	}
}
