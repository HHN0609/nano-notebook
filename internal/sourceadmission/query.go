package sourceadmission

import (
	"sort"
	"strings"
	"time"
)

const maxAdmissionQueries = 3

type StableIdentifier struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

type Profile struct {
	InputKind         string             `json:"input_kind"`
	Title             string             `json:"title"`
	Author            string             `json:"author,omitempty"`
	Publisher         string             `json:"publisher,omitempty"`
	OriginalURL       string             `json:"original_url,omitempty"`
	FinalURL          string             `json:"final_url,omitempty"`
	ContentSHA256     string             `json:"content_sha256"`
	ArtifactSHA256    string             `json:"artifact_sha256,omitempty"`
	PublicationDate   *time.Time         `json:"publication_date,omitempty"`
	StableIdentifiers []StableIdentifier `json:"stable_identifiers"`
}

// BuildQueries returns the bounded, deterministic query sequence used for
// public Source admission. It deliberately does not invent query text.
func BuildQueries(profile Profile) []string {
	queries := make([]string, 0, maxAdmissionQueries)
	seen := make(map[string]struct{}, maxAdmissionQueries)
	appendQuery := func(value string) {
		value = boundedQueryTerm(value)
		if value == "" || len(queries) == maxAdmissionQueries {
			return
		}
		query := `"` + value + `"`
		identity := strings.ToLower(query)
		if _, ok := seen[identity]; ok {
			return
		}
		seen[identity] = struct{}{}
		queries = append(queries, query)
	}

	if identifier, ok := strongestStableIdentifier(profile.StableIdentifiers); ok {
		appendQuery(identifier.Value)
	}
	title := boundedQueryTerm(profile.Title)
	if informativeTitle(title) {
		appendQuery(title)
		qualifier := boundedQueryTerm(profile.Author)
		if qualifier == "" {
			qualifier = boundedQueryTerm(profile.Publisher)
		}
		if qualifier != "" && !strings.EqualFold(title, qualifier) && len(queries) < maxAdmissionQueries {
			query := `"` + title + `" "` + qualifier + `"`
			if _, ok := seen[strings.ToLower(query)]; !ok {
				queries = append(queries, query)
			}
		}
	}
	return queries
}

func strongestStableIdentifier(identifiers []StableIdentifier) (StableIdentifier, bool) {
	candidates := append([]StableIdentifier(nil), identifiers...)
	sort.Slice(candidates, func(left, right int) bool {
		leftPriority := stableIdentifierPriority(candidates[left].Kind)
		rightPriority := stableIdentifierPriority(candidates[right].Kind)
		if leftPriority != rightPriority {
			return leftPriority < rightPriority
		}
		return strings.ToLower(strings.TrimSpace(candidates[left].Value)) < strings.ToLower(strings.TrimSpace(candidates[right].Value))
	})
	seen := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		candidate.Kind = strings.ToLower(strings.TrimSpace(candidate.Kind))
		candidate.Value = boundedQueryTerm(candidate.Value)
		identity := candidate.Kind + "\x00" + strings.ToLower(candidate.Value)
		if stableIdentifierPriority(candidate.Kind) == 100 || candidate.Value == "" {
			continue
		}
		if _, ok := seen[identity]; ok {
			continue
		}
		seen[identity] = struct{}{}
		return candidate, true
	}
	return StableIdentifier{}, false
}

func stableIdentifierPriority(kind string) int {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "doi":
		return 0
	case "isbn":
		return 1
	case "report":
		return 2
	default:
		return 100
	}
}

func boundedQueryTerm(value string) string {
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	value = strings.ReplaceAll(value, `"`, "")
	runes := []rune(value)
	if len(runes) > 240 {
		value = string(runes[:240])
	}
	return strings.TrimSpace(value)
}

func informativeTitle(value string) bool {
	if len([]rune(value)) < 8 {
		return false
	}
	switch strings.ToLower(value) {
	case "home page", "homepage", "untitled document", "document", "download":
		return false
	default:
		return true
	}
}
