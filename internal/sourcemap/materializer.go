package sourcemap

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/huangxinxinyu/nano-notebook/internal/normalize"
)

type Config struct {
	MaxPages       int
	MaxOutputBytes int64
}

type MaterializeRequest struct {
	SourceID       string
	NotebookID     string
	RevisionID     string
	OriginalSHA256 string
	Payload        []byte
	Normalized     normalize.Artifact
}

type PersistInput struct {
	NotebookID string
	ObjectKey  string
	Artifact   Artifact
}

type Record struct {
	ID             string
	SourceID       string
	NotebookID     string
	RevisionID     string
	ObjectKey      string
	ArtifactSHA256 string
	ArtifactBytes  int
	ParserIdentity string
	ParserVersion  string
	ParserPolicyID string
	NavigationKind NavigationKind
	Confidence     Confidence
	PageCount      int
	EntryCount     int
}

type Repository interface {
	Append(context.Context, PersistInput) (Record, bool, error)
}

type objectWriter interface {
	Put(context.Context, string, []byte) error
	Delete(context.Context, string) error
}

type Materializer struct {
	parser     Adapter
	repository Repository
	objects    objectWriter
	config     Config
}

func NewMaterializer(parser Adapter, repository Repository, objects objectWriter, config Config) (*Materializer, error) {
	if repository == nil || objects == nil || config.MaxPages < 1 || config.MaxPages > MaxParserPages ||
		config.MaxOutputBytes < 1 || config.MaxOutputBytes > MaxParserOutput {
		return nil, errors.New("invalid Source Map Materializer")
	}
	return &Materializer{parser: parser, repository: repository, objects: objects, config: config}, nil
}

func (m *Materializer) Materialize(ctx context.Context, request MaterializeRequest) (Record, error) {
	if m == nil || m.repository == nil || m.objects == nil {
		return Record{}, errors.New("nil Source Map Materializer")
	}
	request.SourceID = strings.TrimSpace(request.SourceID)
	request.NotebookID = strings.TrimSpace(request.NotebookID)
	request.RevisionID = strings.TrimSpace(request.RevisionID)
	digest := sha256.Sum256(request.Payload)
	if request.SourceID == "" || request.NotebookID == "" || request.RevisionID == "" || request.Normalized.SourceID != request.SourceID ||
		request.Normalized.Format != "pdf" || int64(len(request.Payload)) < 1 || request.OriginalSHA256 != hex.EncodeToString(digest[:]) {
		return Record{}, errors.New("invalid Source Map materialization request")
	}
	build := BuildInput{
		SourceID: request.SourceID, RevisionID: request.RevisionID, OriginalSHA256: request.OriginalSHA256,
		Normalized: request.Normalized,
	}
	if m.parser != nil {
		parsed, err := m.parser.ParsePDF(ctx, ParseRequest{
			SchemaVersion: 1, SourceID: request.SourceID, InputSHA256: request.OriginalSHA256, InputBytes: int64(len(request.Payload)),
			ParserPolicyID: ParserPolicyNoOCR, OCR: false, MaxPages: m.config.MaxPages, MaxOutputBytes: m.config.MaxOutputBytes,
		}, request.Payload)
		if err != nil {
			if ctx.Err() != nil {
				return Record{}, ctx.Err()
			}
			build.ParserFailureCode = "parser_unavailable"
		} else if err := ValidateDocument(ParseRequest{
			SchemaVersion: 1, SourceID: request.SourceID, InputSHA256: request.OriginalSHA256, InputBytes: int64(len(request.Payload)),
			ParserPolicyID: ParserPolicyNoOCR, MaxPages: m.config.MaxPages, MaxOutputBytes: m.config.MaxOutputBytes,
		}, parsed.Document); err != nil {
			build.ParserFailureCode = "parser_invalid"
		} else {
			build.Parser = &parsed
		}
	} else {
		build.ParserFailureCode = "parser_not_configured"
	}
	artifact, err := BuildArtifact(build)
	if err != nil {
		return Record{}, err
	}
	objectKey := fmt.Sprintf("sources/%s/evidence/%s/source-map/%s.json", request.SourceID, request.RevisionID, artifact.Map.MapID)
	if err := m.objects.Put(ctx, objectKey, artifact.CanonicalJSON); err != nil {
		return Record{}, err
	}
	record, _, err := m.repository.Append(ctx, PersistInput{NotebookID: request.NotebookID, ObjectKey: objectKey, Artifact: artifact})
	if err != nil {
		cleanupCtx := context.WithoutCancel(ctx)
		if cleanupErr := m.objects.Delete(cleanupCtx, objectKey); cleanupErr != nil {
			return Record{}, errors.Join(err, fmt.Errorf("clean Source Map orphan: %w", cleanupErr))
		}
		return Record{}, err
	}
	return record, nil
}

func recordFromPersistInput(input PersistInput) Record {
	value := input.Artifact.Map
	return Record{
		ID: value.MapID, SourceID: value.SourceID, NotebookID: input.NotebookID, RevisionID: value.RevisionID,
		ObjectKey: input.ObjectKey, ArtifactSHA256: input.Artifact.SHA256, ArtifactBytes: len(input.Artifact.CanonicalJSON),
		ParserIdentity: value.ParserIdentity, ParserVersion: value.ParserVersion, ParserPolicyID: value.ParserPolicyID,
		NavigationKind: value.NavigationKind, Confidence: value.Confidence, PageCount: value.PageCount, EntryCount: len(value.Entries),
	}
}

func sha256Hex(payload []byte) string {
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}
