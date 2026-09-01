package sourcemap

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/huangxinxinyu/nano-notebook/internal/objectstore"
)

type parserStub struct {
	result ParseResult
	err    error
	calls  int
}

func (p *parserStub) ParsePDF(context.Context, ParseRequest, []byte) (ParseResult, error) {
	p.calls++
	return p.result, p.err
}

type repositoryStub struct {
	record Record
	err    error
	input  PersistInput
	calls  int
}

func (r *repositoryStub) Append(_ context.Context, input PersistInput) (Record, bool, error) {
	r.calls++
	r.input = input
	if r.err != nil {
		return Record{}, false, r.err
	}
	if r.record.ID == "" {
		r.record = recordFromPersistInput(input)
	}
	return r.record, false, nil
}

func TestMaterializerPersistsRichImmutableSourceMap(t *testing.T) {
	payload := []byte("%PDF parser payload")
	document := parserDocument(2)
	document.InputSHA256 = sha256Hex(payload)
	parser := &parserStub{result: ParseResult{Document: document, CanonicalJSON: []byte(`{"rich":true}`), SHA256: strings.Repeat("b", 64)}}
	repository := &repositoryStub{}
	objects := objectstore.NewMemoryStore()
	materializer, err := NewMaterializer(parser, repository, objects, Config{MaxPages: 500, MaxOutputBytes: 16 << 20})
	if err != nil {
		t.Fatal(err)
	}

	normalized := normalizedPDF(2, false)
	normalized.SourceID = "src_pdf"
	record, err := materializer.Materialize(context.Background(), MaterializeRequest{
		SourceID: "src_pdf", NotebookID: "nb_pdf", RevisionID: "rev_pdf", OriginalSHA256: sha256Hex(payload),
		Payload: payload, Normalized: normalized,
	})
	if err != nil {
		t.Fatal(err)
	}
	if parser.calls != 1 || repository.calls != 1 || record.NavigationKind != NavigationEmbeddedOutline && record.NavigationKind != NavigationPageSamples {
		t.Fatalf("parser/repository/record=%d/%d/%+v", parser.calls, repository.calls, record)
	}
	stored, err := objects.Get(context.Background(), repository.input.ObjectKey, 16<<20)
	if err != nil || string(stored) != string(repository.input.Artifact.CanonicalJSON) || !strings.Contains(repository.input.ObjectKey, "/source-map/") {
		t.Fatalf("stored=%s key=%q err=%v", stored, repository.input.ObjectKey, err)
	}
}

func TestMaterializerParserFailurePersistsDeterministicPageSamples(t *testing.T) {
	payload := []byte("%PDF scanned payload")
	parser := &parserStub{err: errors.New("sidecar unavailable")}
	repository := &repositoryStub{}
	objects := objectstore.NewMemoryStore()
	materializer, _ := NewMaterializer(parser, repository, objects, Config{MaxPages: 500, MaxOutputBytes: 16 << 20})

	first, err := materializer.Materialize(context.Background(), MaterializeRequest{
		SourceID: "src_scan", NotebookID: "nb_scan", RevisionID: "rev_scan", OriginalSHA256: sha256Hex(payload),
		Payload: payload, Normalized: normalizedPDF(9, true),
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.NavigationKind != NavigationPageSamples || first.Confidence != ConfidenceLow || repository.input.Artifact.Map.ParserArtifactSHA256 != "" || len(repository.input.Artifact.Map.Warnings) == 0 {
		t.Fatalf("record/input=%+v/%+v", first, repository.input)
	}
}

func TestMaterializerDoesNotAppendMetadataWhenObjectPersistenceFails(t *testing.T) {
	payload := []byte("%PDF payload")
	repository := &repositoryStub{}
	materializer, _ := NewMaterializer(nil, repository, failingObjectStore{err: errors.New("object unavailable")}, Config{MaxPages: 500, MaxOutputBytes: 16 << 20})
	if _, err := materializer.Materialize(context.Background(), MaterializeRequest{
		SourceID: "src_scan", NotebookID: "nb_scan", RevisionID: "rev_scan", OriginalSHA256: sha256Hex(payload),
		Payload: payload, Normalized: normalizedPDF(2, true),
	}); err == nil || repository.calls != 0 {
		t.Fatalf("err=%v repository_calls=%d", err, repository.calls)
	}
}

func TestMaterializerCleansOrphanObjectWhenMetadataAppendFails(t *testing.T) {
	payload := []byte("%PDF payload")
	repository := &repositoryStub{err: ErrPersistenceConflict}
	objects := objectstore.NewMemoryStore()
	materializer, _ := NewMaterializer(nil, repository, objects, Config{MaxPages: 500, MaxOutputBytes: 16 << 20})
	if _, err := materializer.Materialize(context.Background(), MaterializeRequest{
		SourceID: "src_scan", NotebookID: "nb_scan", RevisionID: "rev_scan", OriginalSHA256: sha256Hex(payload),
		Payload: payload, Normalized: normalizedPDF(2, true),
	}); !errors.Is(err, ErrPersistenceConflict) || objects.Len() != 0 {
		t.Fatalf("err=%v orphan_objects=%d", err, objects.Len())
	}
}

type failingObjectStore struct{ err error }

func (s failingObjectStore) Put(context.Context, string, []byte) error { return s.err }
func (s failingObjectStore) Delete(context.Context, string) error      { return nil }
