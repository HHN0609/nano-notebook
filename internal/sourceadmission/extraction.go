package sourceadmission

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/huangxinxinyu/nano-notebook/internal/normalize"
)

// ObserveExtraction converts an already validated normalized Artifact into
// deterministic quality signals. The Artifact remains the authority.
func ObserveExtraction(artifact normalize.Artifact) (ExtractionObservation, error) {
	if err := normalize.Validate(artifact); err != nil {
		return ExtractionObservation{}, err
	}
	invalidCharacters := 0
	for _, character := range artifact.Text {
		if character == unicode.ReplacementChar || (unicode.IsControl(character) && character != '\n' && character != '\r' && character != '\t') {
			invalidCharacters++
		}
	}
	totalCharacters := utf8.RuneCountInString(artifact.Text)
	seenBlocks := make(map[string]struct{}, len(artifact.Blocks))
	repeatedBlocks := 0
	for _, block := range artifact.Blocks {
		identity := strings.Join(strings.Fields(strings.ToLower(block.Text)), " ")
		if _, ok := seenBlocks[identity]; ok {
			repeatedBlocks++
		} else {
			seenBlocks[identity] = struct{}{}
		}
	}
	return ExtractionObservation{
		CoverageStatus:        artifact.Coverage.Status,
		TotalRunes:            artifact.Coverage.TotalRunes,
		BlockCount:            len(artifact.Blocks),
		GapCount:              len(artifact.Coverage.Gaps),
		InvalidCharacterRatio: rounded(float64(invalidCharacters) / float64(totalCharacters)),
		RepeatedBlockRatio:    rounded(float64(repeatedBlocks) / float64(len(artifact.Blocks))),
	}, nil
}
