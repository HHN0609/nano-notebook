package sourceadmission

import (
	"reflect"
	"testing"
)

func TestBuildQueriesUsesStrongestBoundedIdentitySignals(t *testing.T) {
	queries := BuildQueries(Profile{
		Title:     "A Deterministic Source Admission System",
		Publisher: "Nano Research Lab",
		StableIdentifiers: []StableIdentifier{
			{Kind: "doi", Value: "10.1234/nano.2026.7"},
		},
	})

	want := []string{
		`"10.1234/nano.2026.7"`,
		`"A Deterministic Source Admission System"`,
		`"A Deterministic Source Admission System" "Nano Research Lab"`,
	}
	if !reflect.DeepEqual(queries, want) {
		t.Fatalf("BuildQueries()=%#v want=%#v", queries, want)
	}
}

func TestBuildQueriesOmitsDuplicateAndLowInformationQueries(t *testing.T) {
	queries := BuildQueries(Profile{
		Title:     "Home",
		Author:    "Home",
		Publisher: "Home",
		StableIdentifiers: []StableIdentifier{
			{Kind: "doi", Value: "10.1234/nano"},
			{Kind: "doi", Value: "10.1234/nano"},
			{Kind: "isbn", Value: "978-1-4028-9462-6"},
		},
	})

	want := []string{`"10.1234/nano"`}
	if !reflect.DeepEqual(queries, want) {
		t.Fatalf("BuildQueries()=%#v want=%#v", queries, want)
	}
}

func TestBuildQueriesReservesTitleQueriesAfterStrongestStableIdentifier(t *testing.T) {
	queries := BuildQueries(Profile{
		Title:     "A Deterministic Source Admission System",
		Publisher: "Nano Research Lab",
		StableIdentifiers: []StableIdentifier{
			{Kind: "isbn", Value: "9781402894626"},
			{Kind: "doi", Value: "10.1234/nano.2026.7"},
			{Kind: "report", Value: "NANO-2026-07"},
		},
	})

	want := []string{
		`"10.1234/nano.2026.7"`,
		`"A Deterministic Source Admission System"`,
		`"A Deterministic Source Admission System" "Nano Research Lab"`,
	}
	if !reflect.DeepEqual(queries, want) {
		t.Fatalf("BuildQueries()=%#v want=%#v", queries, want)
	}
}
