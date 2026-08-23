package sourceadmission

import (
	"reflect"
	"testing"
)

func TestExtractStableIdentifiersFindsAndCanonicalizesDOIAndISBN(t *testing.T) {
	identifiers := ExtractStableIdentifiers(
		"Evidence Report doi:10.1000/XYZ.123",
		"https://doi.org/10.1000/xyz.123?utm_source=example",
		"Print edition ISBN 978-1-4028-9462-6. Duplicate DOI 10.1000/xyz.123.",
	)

	want := []StableIdentifier{
		{Kind: "doi", Value: "10.1000/xyz.123"},
		{Kind: "isbn", Value: "9781402894626"},
	}
	if !reflect.DeepEqual(identifiers, want) {
		t.Fatalf("ExtractStableIdentifiers()=%#v want=%#v", identifiers, want)
	}
}

func TestExtractStableIdentifiersRejectsInvalidISBNChecksum(t *testing.T) {
	identifiers := ExtractStableIdentifiers("ISBN 978-1-4028-9462-7", "", "")
	if len(identifiers) != 0 {
		t.Fatalf("identifiers=%#v want none", identifiers)
	}
}
