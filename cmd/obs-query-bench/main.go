package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/huangxinxinyu/nano-notebook/internal/obsbench"
)

type config struct {
	BaseURL     string
	Token       string
	Seed        string
	Roots       uint64
	Requests    int
	Concurrency int
	Timeout     time.Duration
}

func parseConfig(args []string) (config, error) {
	flags := flag.NewFlagSet("obs-query-bench", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var parsed config
	flags.StringVar(&parsed.BaseURL, "base-url", "", "Collector base URL")
	flags.StringVar(&parsed.Token, "token", "", "Collector query token")
	flags.StringVar(&parsed.Seed, "seed", "", "seed of the retained fixture dataset")
	flags.Uint64Var(&parsed.Roots, "roots", 0, "retained root Agent Run count")
	flags.IntVar(&parsed.Requests, "requests", 0, "measured product query count")
	flags.IntVar(&parsed.Concurrency, "concurrency", 0, "parallel query workers")
	flags.DurationVar(&parsed.Timeout, "timeout", 10*time.Minute, "overall query run timeout")
	if err := flags.Parse(args); err != nil {
		return config{}, err
	}
	parsed.BaseURL = strings.TrimSpace(parsed.BaseURL)
	parsed.Token = strings.TrimSpace(parsed.Token)
	parsed.Seed = strings.TrimSpace(parsed.Seed)
	if parsed.BaseURL == "" || parsed.Token == "" || parsed.Seed == "" || parsed.Roots < 100 ||
		parsed.Requests < 1 || parsed.Concurrency < 1 || parsed.Concurrency > 256 || parsed.Timeout <= 0 {
		return config{}, errors.New("observability query benchmark configuration is incomplete or unbounded")
	}
	return parsed, nil
}

type output struct {
	SchemaVersion int                         `json:"schema_version"`
	Corpus        string                      `json:"corpus"`
	Seed          string                      `json:"seed"`
	Roots         uint64                      `json:"roots"`
	Requests      int                         `json:"requests"`
	Concurrency   int                         `json:"concurrency"`
	ElapsedMS     float64                     `json:"elapsed_ms"`
	Result        obsbench.ProductQueryResult `json:"result"`
}

func main() {
	parsed, err := parseConfig(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, parsed, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, config config, writer io.Writer) error {
	plan, err := obsbench.NewProductQueryPlanV1(obsbench.ReferenceWorkloadV1(), config.Seed, config.Roots)
	if err != nil {
		return err
	}
	runCtx, cancel := context.WithTimeout(ctx, config.Timeout)
	defer cancel()
	startedAt := time.Now()
	result, err := obsbench.RunProductQueries(runCtx, obsbench.ProductQueryRunnerConfig{
		BaseURL: config.BaseURL, Token: config.Token, Plan: plan,
		Requests: config.Requests, Concurrency: config.Concurrency,
		HTTPClient: &http.Client{Timeout: 10 * time.Second},
	})
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(writer)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(output{
		SchemaVersion: 1, Corpus: "trace-product-query-v1", Seed: config.Seed, Roots: config.Roots,
		Requests: config.Requests, Concurrency: config.Concurrency,
		ElapsedMS: float64(time.Since(startedAt)) / float64(time.Millisecond), Result: result,
	})
}
