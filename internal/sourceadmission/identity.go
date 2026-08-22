package sourceadmission

import (
	"regexp"
	"sort"
	"strings"
)

const maxIdentifierScanBytes = 1 << 20

var (
	doiPattern  = regexp.MustCompile(`(?i)\b10\.\d{4,9}/[-._;()/:A-Z0-9]+`)
	isbnPattern = regexp.MustCompile(`(?i)\bISBN(?:-1[03])?\s*:?\s*([0-9X][0-9X -]{8,20}[0-9X])`)
)

// ExtractStableIdentifiers applies bounded mechanical parsers only. It does
// not ask a model to infer identifiers from surrounding prose.
func ExtractStableIdentifiers(title, publicURL, primaryText string) []StableIdentifier {
	if len(primaryText) > maxIdentifierScanBytes {
		primaryText = primaryText[:maxIdentifierScanBytes]
	}
	input := strings.Join([]string{title, publicURL, primaryText}, "\n")
	seen := make(map[string]StableIdentifier)
	for _, match := range doiPattern.FindAllString(input, -1) {
		value := strings.ToLower(strings.TrimRight(strings.TrimSpace(match), ".,;:"))
		seen["doi\x00"+value] = StableIdentifier{Kind: "doi", Value: value}
	}
	for _, match := range isbnPattern.FindAllStringSubmatch(input, -1) {
		value := strings.NewReplacer("-", "", " ", "").Replace(strings.ToUpper(match[1]))
		if validISBN(value) {
			seen["isbn\x00"+value] = StableIdentifier{Kind: "isbn", Value: value}
		}
	}
	identifiers := make([]StableIdentifier, 0, len(seen))
	for _, identifier := range seen {
		identifiers = append(identifiers, identifier)
	}
	sort.Slice(identifiers, func(left, right int) bool {
		leftPriority := stableIdentifierPriority(identifiers[left].Kind)
		rightPriority := stableIdentifierPriority(identifiers[right].Kind)
		if leftPriority != rightPriority {
			return leftPriority < rightPriority
		}
		return identifiers[left].Value < identifiers[right].Value
	})
	return identifiers
}

func validISBN(value string) bool {
	switch len(value) {
	case 10:
		total := 0
		for index, character := range value {
			digit := int(character - '0')
			if index == 9 && character == 'X' {
				digit = 10
			} else if character < '0' || character > '9' {
				return false
			}
			total += (10 - index) * digit
		}
		return total%11 == 0
	case 13:
		total := 0
		for index, character := range value {
			if character < '0' || character > '9' {
				return false
			}
			digit := int(character - '0')
			if index%2 == 1 {
				digit *= 3
			}
			total += digit
		}
		return total%10 == 0
	default:
		return false
	}
}
