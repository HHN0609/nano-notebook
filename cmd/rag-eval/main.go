package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/huangxinxinyu/nano-notebook/internal/models"
	"github.com/huangxinxinyu/nano-notebook/internal/objectstore"
	"github.com/huangxinxinyu/nano-notebook/internal/qdrantstore"
	"github.com/huangxinxinyu/nano-notebook/internal/rageval"
	"github.com/huangxinxinyu/nano-notebook/internal/retrieval"
	"github.com/huangxinxinyu/nano-notebook/internal/source"
	"github.com/huangxinxinyu/nano-notebook/internal/sourceprojection"
	"github.com/jackc/pgx/v5/pgxpool"
)

var errEvalGatesFailed = errors.New("RAG Eval gates failed")

func main() {
	if len(os.Args) > 1 && os.Args[1] == "sweep" {
		if err := runSweep(os.Args[2:], os.Stdout); err != nil {
			log.Fatal(err)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "units" {
		if err := runUnits(os.Args[2:], os.Stdout); err != nil {
			log.Fatal(err)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "ingest-samples" {
		if err := runIngestSamples(os.Args[2:], os.Stdout); err != nil {
			log.Fatal(err)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "build-suite" {
		if err := runBuildSuite(os.Args[2:], os.Stdout); err != nil {
			log.Fatal(err)
		}
		return
	}
	if err := run(os.Args[1:], os.Stdout); err != nil {
		log.Fatal(err)
	}
}

type evidenceUnitListing struct {
	ID      string `json:"id"`
	Ordinal int    `json:"ordinal"`
	Kind    string `json:"kind"`
	Preview string `json:"preview"`
}

type retrievalSourceListing struct {
	CaseID   string                `json:"case_id"`
	SourceID string                `json:"source_id"`
	Units    []evidenceUnitListing `json:"units"`
}

func runUnits(args []string, output io.Writer) error {
	flags := flag.NewFlagSet("rag-eval units", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	manifestPath := flags.String("manifest", "", "path to the retrieval sweep live Source manifest")
	databaseURL := flags.String("database-url", "", "PostgreSQL URL")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*manifestPath) == "" || strings.TrimSpace(*databaseURL) == "" {
		return errors.New("-manifest and -database-url are required")
	}
	var manifest rageval.RetrievalSourceManifest
	if err := decodeStrictFile(*manifestPath, &manifest); err != nil {
		return fmt.Errorf("load retrieval sweep Source manifest: %w", err)
	}
	pool, err := pgxpool.New(context.Background(), *databaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	listings := make([]retrievalSourceListing, 0, len(manifest.Cases))
	for _, item := range manifest.Cases {
		notebookID := sourceCaseNotebookID(manifest, item)
		rows, err := pool.Query(context.Background(), `
			select u.id,u.ordinal,u.kind,left(u.text_content,100)
			from source_evidence_units u
			where u.source_id=$1 and u.revision_id=$2 and u.notebook_id=$3
			order by u.ordinal
		`, item.SourceID, item.EvidenceRevisionID, notebookID)
		if err != nil {
			return err
		}
		units := make([]evidenceUnitListing, 0)
		for rows.Next() {
			var listing evidenceUnitListing
			if err := rows.Scan(&listing.ID, &listing.Ordinal, &listing.Kind, &listing.Preview); err != nil {
				rows.Close()
				return err
			}
			units = append(units, listing)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		listings = append(listings, retrievalSourceListing{CaseID: item.CaseID, SourceID: item.SourceID, Units: units})
	}
	payload, err := json.MarshalIndent(map[string][]retrievalSourceListing{"cases": listings}, "", "  ")
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintln(output, string(payload)); err != nil {
		return err
	}
	return nil
}

type sampleRecord struct {
	CaseID       string `json:"case_id"`
	DatasetID    string `json:"dataset_id"`
	QueryID      string `json:"query_id"`
	Query        string `json:"query"`
	DocID        string `json:"doc_id"`
	DocText      string `json:"doc_text"`
	SourceRef    string `json:"source_ref"`
	IsDistractor bool   `json:"is_distractor"`
}

func runIngestSamples(args []string, output io.Writer) error {
	flags := flag.NewFlagSet("rag-eval ingest-samples", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	samplesPath := flags.String("samples", "", "path to sampled JSONL from prepare_dataset.py")
	manifestOut := flags.String("manifest-out", "", "path for the source manifest JSON")
	databaseURL := flags.String("database-url", "", "PostgreSQL URL")
	userID := flags.String("user-id", "", "Eval principal user ID")
	notebookID := flags.String("notebook-id", "", "Eval notebook ID")
	indexVersionID := flags.String("index-version-id", "", "Retrieval Index Version ID")
	s3Endpoint := flags.String("s3-endpoint", "127.0.0.1:59000", "S3-compatible endpoint")
	s3AccessKey := flags.String("s3-access-key", "nano", "S3 access key")
	s3SecretKey := flags.String("s3-secret-key", "nano-password", "S3 secret key")
	s3Bucket := flags.String("s3-bucket", "nano-sources", "S3 bucket")
	s3Region := flags.String("s3-region", "us-east-1", "S3 region")
	s3UseTLS := flags.Bool("s3-use-tls", false, "use TLS for S3")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*samplesPath) == "" || strings.TrimSpace(*manifestOut) == "" || strings.TrimSpace(*databaseURL) == "" ||
		strings.TrimSpace(*userID) == "" || strings.TrimSpace(*notebookID) == "" || strings.TrimSpace(*indexVersionID) == "" {
		return errors.New("-samples, -manifest-out, -database-url, -user-id, -notebook-id, and -index-version-id are required")
	}
	records, err := loadSampleRecords(*samplesPath)
	if err != nil {
		return err
	}
	pool, err := pgxpool.New(context.Background(), *databaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	objects, err := objectstore.NewS3Store(objectstore.S3Config{
		Endpoint: *s3Endpoint, AccessKeyID: *s3AccessKey, SecretAccessKey: *s3SecretKey,
		Bucket: *s3Bucket, Region: *s3Region, UseTLS: *s3UseTLS,
	})
	if err != nil {
		return err
	}
	manifest := rageval.RetrievalSourceManifest{
		SchemaVersion: 1, IndexVersionID: *indexVersionID, UserID: *userID, NotebookID: *notebookID,
		Cases: make([]rageval.RetrievalSourceCase, 0, len(records)),
	}
	for index, record := range records {
		caseID := sampleCaseID(record, index)
		payload := []byte(record.DocText)
		digest := sha256.Sum256(payload)
		contentSHA := hex.EncodeToString(digest[:])
		title := caseID + ".txt"
		if !source.ValidFileAdmission(title, source.FormatTXT, "text/plain") {
			return fmt.Errorf("invalid sample Source title %q", title)
		}
		jobID := "srcjob_eval_" + uuid.NewString()
		now := time.Now().UTC().Truncate(time.Microsecond)
		expiresAt := now.Add(15 * time.Minute)
		requestHash := fmt.Sprintf("%x", sha256.Sum256([]byte(caseID+"|"+*notebookID+"|"+contentSHA)))
		intent, err := createEvalUploadIntent(context.Background(), pool, *userID, source.CreateUploadIntentCommand{
			ID: "upl_eval_" + uuid.NewString(), SourceID: "src_eval_" + uuid.NewString(), NotebookID: *notebookID, IdempotencyKey: caseID + ":" + *notebookID,
			RequestHash: requestHash, Title: title, Format: source.FormatTXT, MediaType: "text/plain",
			ByteSize: int64(len(payload)), ContentSHA256: contentSHA,
			ObjectKey: "source-upload-intents/" + uuid.NewString() + "/payload", ExpiresAt: expiresAt,
		})
		if err != nil {
			return fmt.Errorf("create upload intent for %s: %w", caseID, err)
		}
		sourceID := intent.SourceID
		finalKey := "sources/" + sourceID + "/original/" + contentSHA
		if err := objects.Put(context.Background(), finalKey, payload); err != nil {
			return fmt.Errorf("upload final object for %s: %w", caseID, err)
		}
		if err := finalizeEvalUploadIntent(context.Background(), pool, *userID, intent.ID, jobID, finalKey, now); err != nil {
			return fmt.Errorf("finalize upload for %s: %w", caseID, err)
		}
		manifest.Cases = append(manifest.Cases, rageval.RetrievalSourceCase{
			CaseID: caseID, SourceID: sourceID, EvidenceRevisionID: "",
		})
		if (index+1)%10 == 0 {
			fmt.Fprintf(output, "ingested %d/%d samples\n", index+1, len(records))
		}
	}
	payload, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(*manifestOut, payload, 0o644); err != nil {
		return err
	}
	_, err = fmt.Fprintf(output, "wrote %s with %d sources\n", *manifestOut, len(manifest.Cases))
	return err
}

func runBuildSuite(args []string, output io.Writer) error {
	flags := flag.NewFlagSet("rag-eval build-suite", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	samplesPath := flags.String("samples", "", "path to sampled JSONL")
	manifestPath := flags.String("manifest", "", "path to the source manifest JSON")
	manifestOut := flags.String("manifest-out", "", "path for the resolved source manifest JSON")
	databaseURL := flags.String("database-url", "", "PostgreSQL URL")
	indexVersionID := flags.String("index-version-id", "", "Retrieval Index Version ID")
	suiteOut := flags.String("out", "evals/rag/retrieval-sweep-v1.json", "path for the generated retrieval Suite")
	suiteID := flags.String("suite-id", "rag-retrieval-sweep-v1", "retrieval Suite ID")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*samplesPath) == "" || strings.TrimSpace(*manifestPath) == "" || strings.TrimSpace(*databaseURL) == "" ||
		strings.TrimSpace(*indexVersionID) == "" {
		return errors.New("-samples, -manifest, -database-url, and -index-version-id are required")
	}
	records, err := loadSampleRecords(*samplesPath)
	if err != nil {
		return err
	}
	var manifest rageval.RetrievalSourceManifest
	if err := decodeStrictFile(*manifestPath, &manifest); err != nil {
		return fmt.Errorf("load retrieval sweep Source manifest: %w", err)
	}
	pool, err := pgxpool.New(context.Background(), *databaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	byCase := make(map[string]rageval.RetrievalSourceCase, len(manifest.Cases))
	for _, item := range manifest.Cases {
		byCase[item.CaseID] = item
	}
	suite := rageval.RetrievalSuite{
		SchemaVersion: 1, ID: *suiteID, Cases: make([]rageval.RetrievalCase, 0, len(records)),
	}
	for index, record := range records {
		caseID := sampleCaseID(record, index)
		item, ok := byCase[caseID]
		if !ok {
			return fmt.Errorf("source manifest is missing Case %s", caseID)
		}
		notebookID := sourceCaseNotebookID(manifest, item)
		revisionID, err := activeEvidenceRevision(context.Background(), pool, notebookID, item.SourceID)
		if err != nil {
			return fmt.Errorf("resolve %s: %w", caseID, err)
		}
		verified, err := indexBuildVerified(context.Background(), pool, revisionID, *indexVersionID)
		if err != nil {
			return fmt.Errorf("verify %s: %w", caseID, err)
		}
		if !verified {
			return fmt.Errorf("Source %s has no verified build for Index Version %s", item.SourceID, *indexVersionID)
		}
		unitIDs, err := evidenceUnitIDs(context.Background(), pool, notebookID, item.SourceID, revisionID)
		if err != nil {
			return fmt.Errorf("load Evidence Units for %s: %w", caseID, err)
		}
		if len(unitIDs) == 0 {
			return fmt.Errorf("Source %s has no Evidence Units", item.SourceID)
		}
		item.EvidenceRevisionID = revisionID
		byCase[caseID] = item
		if record.IsDistractor {
			// Distractors are admitted as ordinary Sources so they sit in the
			// retrieval candidate pool, but they have no query of their own and
			// must never become an expected answer for any Case.
			continue
		}
		suite.Cases = append(suite.Cases, rageval.RetrievalCase{
			ID: caseID, Question: record.Query, Language: sampleLanguage(record.DatasetID),
			DatasetID: record.DatasetID, SourceRef: record.SourceRef,
			ExpectedEvidenceSets: [][]string{unitIDs},
		})
	}
	payload, err := json.MarshalIndent(suite, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(*suiteOut, payload, 0o644); err != nil {
		return err
	}
	if strings.TrimSpace(*manifestOut) != "" {
		resolved := manifest
		resolved.Cases = make([]rageval.RetrievalSourceCase, 0, len(manifest.Cases))
		for _, original := range manifest.Cases {
			resolved.Cases = append(resolved.Cases, byCase[original.CaseID])
		}
		resolvedPayload, err := json.MarshalIndent(resolved, "", "  ")
		if err != nil {
			return err
		}
		if err := os.WriteFile(*manifestOut, resolvedPayload, 0o644); err != nil {
			return err
		}
		_, err = fmt.Fprintf(output, "wrote %s\n", *manifestOut)
		if err != nil {
			return err
		}
	}
	_, err = fmt.Fprintf(output, "wrote %s with %d cases\n", *suiteOut, len(suite.Cases))
	return err
}

func createEvalUploadIntent(ctx context.Context, pool *pgxpool.Pool, userID string, command source.CreateUploadIntentCommand) (source.UploadIntent, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return source.UploadIntent{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `set local role nano_app`); err != nil {
		return source.UploadIntent{}, err
	}
	if _, err := tx.Exec(ctx, `select set_config('app.principal_id',$1,true)`, userID); err != nil {
		return source.UploadIntent{}, err
	}
	intent, _, err := source.NewStore(tx).CreateUploadIntent(ctx, command)
	if err != nil {
		return source.UploadIntent{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return source.UploadIntent{}, err
	}
	return intent, nil
}

func finalizeEvalUploadIntent(ctx context.Context, pool *pgxpool.Pool, userID, intentID, jobID, finalKey string, now time.Time) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `set local role nano_app`); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `select set_config('app.principal_id',$1,true)`, userID); err != nil {
		return err
	}
	if _, _, err := source.NewStore(tx).FinalizeUploadIntent(ctx, intentID, jobID, finalKey, now); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func loadSampleRecords(path string) ([]sampleRecord, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	records := make([]sampleRecord, 0)
	for scanner.Scan() {
		var record sampleRecord
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			return nil, err
		}
		if strings.TrimSpace(record.DatasetID) == "" || strings.TrimSpace(record.DocText) == "" {
			return nil, errors.New("sample JSONL contains a blank dataset_id or doc_text")
		}
		if !record.IsDistractor && strings.TrimSpace(record.Query) == "" {
			return nil, errors.New("sample JSONL contains a blank query for a non-distractor row")
		}
		records = append(records, record)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return records, nil
}

func sampleCaseID(record sampleRecord, index int) string {
	if strings.TrimSpace(record.CaseID) != "" {
		return record.CaseID
	}
	prefix := "msmarco"
	if strings.Contains(record.DatasetID, "dureader") {
		prefix = "dureader"
	}
	return fmt.Sprintf("%s-%04d", prefix, index+1)
}

func sampleLanguage(datasetID string) string {
	if strings.Contains(datasetID, "dureader") || strings.Contains(datasetID, "cmedqa") {
		return "zh"
	}
	return "en"
}

func sourceCaseNotebookID(manifest rageval.RetrievalSourceManifest, item rageval.RetrievalSourceCase) string {
	if strings.TrimSpace(item.NotebookID) != "" {
		return item.NotebookID
	}
	return manifest.NotebookID
}

func activeEvidenceRevision(ctx context.Context, pool *pgxpool.Pool, notebookID, sourceID string) (string, error) {
	var revisionID string
	err := pool.QueryRow(ctx, `
		select r.id
		from source_evidence_revisions r
		join source_sources s on s.id=r.source_id and s.notebook_id=r.notebook_id
		where r.source_id=$1 and r.notebook_id=$2 and r.status='active' and s.state='ready'
		order by r.revision_no desc
		limit 1
	`, sourceID, notebookID).Scan(&revisionID)
	if err != nil {
		return "", err
	}
	return revisionID, nil
}

func indexBuildVerified(ctx context.Context, pool *pgxpool.Pool, revisionID, indexVersionID string) (bool, error) {
	var verified bool
	if err := pool.QueryRow(ctx, `
		select exists(
			select 1 from retrieval_source_index_builds
			where revision_id=$1 and index_version_id=$2 and status='verified'
		)
	`, revisionID, indexVersionID).Scan(&verified); err != nil {
		return false, err
	}
	return verified, nil
}

func evidenceUnitIDs(ctx context.Context, pool *pgxpool.Pool, notebookID, sourceID, revisionID string) ([]string, error) {
	rows, err := pool.Query(ctx, `
		select id from source_evidence_units
		where source_id=$1 and revision_id=$2 and notebook_id=$3
		order by ordinal
	`, sourceID, revisionID, notebookID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

type sweepConfig struct {
	suitePath        string
	gridPath         string
	manifestPath     string
	outPrefix        string
	databaseURL      string
	bifrostURL       string
	qdrantURL        string
	qdrantAPIKey     string
	qdrantCollection string
	qdrantDimensions int
	executorTimeout  time.Duration
}

func runSweep(args []string, output io.Writer) error {
	config, err := parseSweepArgs(args)
	if err != nil {
		return err
	}
	if strings.TrimSpace(config.manifestPath) == "" || strings.TrimSpace(config.databaseURL) == "" {
		return errors.New("-manifest and -database-url are required for a live retrieval sweep")
	}
	var manifest rageval.RetrievalSourceManifest
	if err := decodeStrictFile(config.manifestPath, &manifest); err != nil {
		return fmt.Errorf("load retrieval sweep Source manifest: %w", err)
	}
	pool, err := pgxpool.New(context.Background(), config.databaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	vectors, err := qdrantstore.New(qdrantstore.Config{
		BaseURL: config.qdrantURL, APIKey: config.qdrantAPIKey, Collection: config.qdrantCollection,
		DenseDimensions: config.qdrantDimensions, RequestTimeout: config.executorTimeout,
		HTTPClient: &http.Client{Timeout: config.executorTimeout},
	})
	if err != nil {
		return err
	}
	model := models.NewBifrostClient(config.bifrostURL, &http.Client{Timeout: config.executorTimeout}, 2048)
	executor, err := rageval.NewRetrievalLiveExecutor(pool, vectors, model, manifest)
	if err != nil {
		return err
	}
	return writeSweepReport(config, output, executor)
}

func runSweepWithExecutor(args []string, output io.Writer, executor rageval.RetrievalExecutor) error {
	config, err := parseSweepArgs(args)
	if err != nil {
		return err
	}
	return writeSweepReport(config, output, executor)
}

func parseSweepArgs(args []string) (sweepConfig, error) {
	flags := flag.NewFlagSet("rag-eval sweep", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var config sweepConfig
	flags.StringVar(&config.suitePath, "suite", "evals/rag/retrieval-sweep-v1.json", "path to the retrieval sweep Suite")
	flags.StringVar(&config.gridPath, "grid", "", "path to the retrieval sweep grid JSON")
	flags.StringVar(&config.manifestPath, "manifest", "", "path to the retrieval sweep live Source manifest")
	flags.StringVar(&config.outPrefix, "out-prefix", "", "output prefix for CSV and JSON reports")
	flags.StringVar(&config.databaseURL, "database-url", "", "PostgreSQL URL used by the live retrieval sweep")
	flags.StringVar(&config.bifrostURL, "bifrost-url", "http://127.0.0.1:56666", "Bifrost model gateway URL")
	flags.StringVar(&config.qdrantURL, "qdrant-url", "http://127.0.0.1:56333", "Qdrant URL")
	flags.StringVar(&config.qdrantAPIKey, "qdrant-api-key", os.Getenv("NANO_QDRANT_API_KEY"), "Qdrant API key")
	flags.StringVar(&config.qdrantCollection, "qdrant-collection", "nano-source-evidence-gemini-2-768-v1", "Qdrant collection")
	flags.IntVar(&config.qdrantDimensions, "qdrant-dimensions", 768, "Qdrant dense vector dimensions")
	flags.DurationVar(&config.executorTimeout, "executor-timeout", 5*time.Minute, "per-search timeout")
	if err := flags.Parse(args); err != nil {
		return config, err
	}
	if strings.TrimSpace(config.gridPath) == "" || strings.TrimSpace(config.outPrefix) == "" {
		return config, errors.New("-grid and -out-prefix are required")
	}
	return config, nil
}

func writeSweepReport(config sweepConfig, output io.Writer, executor rageval.RetrievalExecutor) error {
	var suite rageval.RetrievalSuite
	if err := decodeStrictFile(config.suitePath, &suite); err != nil {
		return fmt.Errorf("load retrieval sweep Suite: %w", err)
	}
	var grid rageval.RetrievalGrid
	if err := decodeStrictFile(config.gridPath, &grid); err != nil {
		return fmt.Errorf("load retrieval sweep grid: %w", err)
	}
	report, err := rageval.RunRetrievalSweep(context.Background(), suite, grid, executor)
	if err != nil {
		return err
	}
	jsonPayload, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	csvPayload, err := report.CSV()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(config.outPrefix), 0o755); err != nil {
		return err
	}
	jsonPath := config.outPrefix + ".json"
	csvPath := config.outPrefix + ".csv"
	if err := os.WriteFile(jsonPath, jsonPayload, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(csvPath, csvPayload, 0o644); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(output, "wrote %s\nwrote %s\n", jsonPath, csvPath); err != nil {
		return err
	}
	return nil
}

func run(args []string, output io.Writer) error {
	flags := flag.NewFlagSet("rag-eval", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	suitePath := flags.String("suite", "evals/rag/sprint6-v1.json", "path to the frozen Eval Suite")
	configPath := flags.String("config", "evals/rag/pinned-config-v1.json", "path to the pinned product configuration")
	observationsPath := flags.String("observations", "", "path to observations emitted by the product Eval Executor")
	executorCommand := flags.String("executor-command", "", "bounded product Case Executor command (JSON stdin/stdout)")
	productRunsPath := flags.String("product-runs", "", "manifest mapping Eval Cases to completed durable product Runs")
	liveProductSourcesPath := flags.String("live-product-sources", "", "manifest mapping Eval Cases to ready fixture Sources")
	executorTimeout := flags.Duration("executor-timeout", 5*time.Minute, "per-Case product Executor timeout")
	bifrostURL := flags.String("bifrost-url", "http://127.0.0.1:56666", "Bifrost model gateway URL for live product Eval")
	qdrantURL := flags.String("qdrant-url", "http://127.0.0.1:56333", "Qdrant URL for live product Eval")
	qdrantAPIKey := flags.String("qdrant-api-key", os.Getenv("NANO_QDRANT_API_KEY"), "Qdrant API key for live product Eval")
	qdrantCollection := flags.String("qdrant-collection", "nano-source-evidence-gemini-2-768-v1", "Qdrant collection for live product Eval")
	databaseURL := flags.String("database-url", "", "PostgreSQL URL used to record and promote a passing candidate")
	evalRunID := flags.String("eval-run-id", "", "durable Eval Run identity")
	versionID := flags.String("index-version-id", "", "candidate Retrieval Index Version identity")
	createCandidate := flags.Bool("create-candidate", false, "create the live Eval candidate from the pinned configuration when it does not exist")
	if err := flags.Parse(args); err != nil {
		return err
	}
	modes := 0
	for _, value := range []string{*observationsPath, *executorCommand, *productRunsPath, *liveProductSourcesPath} {
		if strings.TrimSpace(value) != "" {
			modes++
		}
	}
	if modes != 1 {
		return errors.New("exactly one of -observations, -executor-command, -product-runs, or -live-product-sources is required")
	}
	if strings.TrimSpace(*evalRunID) != "" && (strings.TrimSpace(*observationsPath) != "" || strings.TrimSpace(*productRunsPath) != "") {
		return errors.New("only a live or bounded product Executor can authorize Retrieval Index promotion")
	}
	var suite rageval.Suite
	if err := decodeStrictFile(*suitePath, &suite); err != nil {
		return fmt.Errorf("load Eval Suite: %w", err)
	}
	var config rageval.PinnedConfig
	if err := decodeStrictFile(*configPath, &config); err != nil {
		return fmt.Errorf("load pinned configuration: %w", err)
	}
	var executor rageval.Executor
	var pool *pgxpool.Pool
	var err error
	if strings.TrimSpace(*observationsPath) != "" {
		var observations []rageval.Observation
		if err := decodeStrictFile(*observationsPath, &observations); err != nil {
			return fmt.Errorf("load product observations: %w", err)
		}
		executor, err = newObservationExecutor(observations)
	} else if strings.TrimSpace(*executorCommand) != "" {
		executor, err = rageval.NewCommandExecutor(rageval.CommandExecutorConfig{
			Command: *executorCommand, Timeout: *executorTimeout, MaxOutputBytes: 8 << 20,
		})
	} else if strings.TrimSpace(*productRunsPath) != "" {
		if strings.TrimSpace(*databaseURL) == "" || strings.TrimSpace(*versionID) == "" {
			return errors.New("-database-url and -index-version-id are required with -product-runs")
		}
		var manifest rageval.ProductRunManifest
		if err := decodeStrictFile(*productRunsPath, &manifest); err != nil {
			return fmt.Errorf("load product Run manifest: %w", err)
		}
		if manifest.IndexVersionID != *versionID {
			return errors.New("product Run manifest Index Version does not match -index-version-id")
		}
		pool, err = pgxpool.New(context.Background(), *databaseURL)
		if err == nil {
			executor, err = rageval.NewProductRunExecutor(pool, manifest)
		}
	} else {
		if strings.TrimSpace(*databaseURL) == "" || strings.TrimSpace(*versionID) == "" {
			return errors.New("-database-url and -index-version-id are required with -live-product-sources")
		}
		var manifest rageval.ProductSourceManifest
		if err := decodeStrictFile(*liveProductSourcesPath, &manifest); err != nil {
			return fmt.Errorf("load live product Source manifest: %w", err)
		}
		if manifest.IndexVersionID != *versionID {
			return errors.New("live product Source manifest Index Version does not match -index-version-id")
		}
		pool, err = pgxpool.New(context.Background(), *databaseURL)
		if err == nil {
			versionStore := retrieval.NewVersionStore(pool)
			if _, versionErr := versionStore.ByID(context.Background(), *versionID); errors.Is(versionErr, retrieval.ErrVersionNotFound) && *createCandidate {
				_, err = versionStore.CreateCandidate(context.Background(), *versionID, config.Index)
			} else if versionErr != nil {
				err = versionErr
			}
			var vectors *qdrantstore.Client
			if err == nil {
				vectors, err = qdrantstore.New(qdrantstore.Config{
					BaseURL: *qdrantURL, APIKey: *qdrantAPIKey, Collection: *qdrantCollection,
					DenseDimensions: config.Index.EmbeddingDimensions, RequestTimeout: *executorTimeout,
					HTTPClient: &http.Client{Timeout: *executorTimeout},
				})
			}
			if err == nil {
				err = vectors.EnsureCollection(context.Background())
			}
			model := models.NewBifrostClient(*bifrostURL, &http.Client{Timeout: *executorTimeout}, 2048)
			if err == nil {
				_, err = sourceprojection.NewReindexer(pool, vectors, model).ReindexVersion(context.Background(), *versionID)
			}
			if err == nil {
				executor, err = rageval.NewLiveProductExecutor(pool, vectors, model, manifest)
			}
		}
	}
	if err != nil {
		if pool != nil {
			pool.Close()
		}
		return err
	}
	if pool != nil {
		defer pool.Close()
	}
	ctx := context.Background()
	recording := strings.TrimSpace(*evalRunID) != ""
	var report rageval.Report
	if recording {
		if strings.TrimSpace(*databaseURL) == "" || strings.TrimSpace(*evalRunID) == "" || strings.TrimSpace(*versionID) == "" {
			return errors.New("-database-url, -eval-run-id, and -index-version-id are required together")
		}
		if pool == nil {
			var poolErr error
			pool, poolErr = pgxpool.New(ctx, *databaseURL)
			if poolErr != nil {
				return poolErr
			}
			defer pool.Close()
		}
		report, err = rageval.EvaluateRecordAndPromote(ctx, *evalRunID, *versionID, suite, config, executor, retrieval.NewVersionStore(pool))
	} else {
		report, err = rageval.Evaluate(ctx, suite, config, executor)
	}
	if err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintln(output, string(encoded)); err != nil {
		return err
	}
	if report.Status != retrieval.EvalPassed {
		return errEvalGatesFailed
	}
	return nil
}

type observationExecutor struct {
	byCase map[string]rageval.Observation
}

func newObservationExecutor(observations []rageval.Observation) (*observationExecutor, error) {
	if len(observations) == 0 {
		return nil, errors.New("product observations are empty")
	}
	executor := &observationExecutor{byCase: make(map[string]rageval.Observation, len(observations))}
	for _, observation := range observations {
		if strings.TrimSpace(observation.CaseID) == "" {
			return nil, errors.New("product observation Case identity is empty")
		}
		if _, duplicate := executor.byCase[observation.CaseID]; duplicate {
			return nil, errors.New("product observation Case identity is duplicated")
		}
		executor.byCase[observation.CaseID] = observation
	}
	return executor, nil
}

func (e *observationExecutor) ExecuteCase(_ context.Context, evalCase rageval.Case, _ rageval.PinnedConfig) (rageval.Observation, error) {
	observation, ok := e.byCase[evalCase.ID]
	if !ok {
		return rageval.Observation{}, fmt.Errorf("product observation for Case %q is missing", evalCase.ID)
	}
	return observation, nil
}

func decodeStrictFile(path string, target any) error {
	payload, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("JSON contains trailing content")
	}
	return nil
}
