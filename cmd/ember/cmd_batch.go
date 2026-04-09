package main

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/scttfrdmn/ember/core/intent"
	"github.com/scttfrdmn/ember/pkg/batch"
	"github.com/scttfrdmn/ember/pkg/sdk"
)

// batchInputLine is the NDJSON schema for one stdin job.
type batchInputLine struct {
	ID          string          `json:"id"`
	WASMB64     string          `json:"wasm_b64"`
	ManifestB64 string          `json:"manifest_b64"`
	Fn          string          `json:"fn"`
	Args        []uint64        `json:"args"`
}

func runBatch(args []string) error {
	fs := flag.NewFlagSet("batch", flag.ContinueOnError)
	concurrency := fs.Int("concurrency", 0, "max parallel jobs (default: NumCPU)")
	timeout := fs.Duration("timeout", 30*time.Second, "total execution timeout")
	format := fs.String("format", "json", "output format: json or tsv")

	if err := fs.Parse(args); err != nil {
		return err
	}
	if *format != "json" && *format != "tsv" {
		return fmt.Errorf("--format must be 'json' or 'tsv'")
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	s := sdk.New()
	runner := batch.New(s)
	runner.MaxConcurrency = *concurrency

	// Read NDJSON jobs from stdin with a 1MB buffer to handle large base64 WASM blobs.
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 1<<20), 1<<20)

	var jobs []sdk.Job
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var input batchInputLine
		if err := json.Unmarshal([]byte(line), &input); err != nil {
			return fmt.Errorf("parse input line: %w", err)
		}

		wasmBytes, err := base64.StdEncoding.DecodeString(input.WASMB64)
		if err != nil {
			return fmt.Errorf("job %s: decode wasm_b64: %w", input.ID, err)
		}
		manifestBytes, err := base64.StdEncoding.DecodeString(input.ManifestB64)
		if err != nil {
			return fmt.Errorf("job %s: decode manifest_b64: %w", input.ID, err)
		}
		var m intent.Manifest
		if err := json.Unmarshal(manifestBytes, &m); err != nil {
			return fmt.Errorf("job %s: decode manifest JSON: %w", input.ID, err)
		}

		jobs = append(jobs, sdk.Job{
			ID:       input.ID,
			Artifact: &sdk.Artifact{WASM: wasmBytes, Manifest: &m},
			Fn:       input.Fn,
			Args:     input.Args,
		})
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read stdin: %w", err)
	}

	results, err := runner.Run(ctx, jobs)
	if err != nil {
		return fmt.Errorf("batch run: %w", err)
	}

	anyErr := false
	w := bufio.NewWriter(os.Stdout)
	defer w.Flush()

	for _, res := range results {
		elapsedMs := float64(res.Elapsed.Microseconds()) / 1000.0
		ok := res.Err == nil
		if !ok {
			anyErr = true
		}

		if *format == "tsv" {
			vals := make([]string, len(res.Values))
			for i, v := range res.Values {
				vals[i] = fmt.Sprintf("%d", v)
			}
			errMsg := ""
			if res.Err != nil {
				errMsg = res.Err.Error()
			}
			fmt.Fprintf(w, "%s\t%v\t%.3f\t%s\t%s\n",
				res.ID, ok, elapsedMs, strings.Join(vals, ","), errMsg)
		} else {
			out := map[string]interface{}{
				"id":         res.ID,
				"ok":         ok,
				"elapsed_ms": elapsedMs,
				"results":    res.Values,
			}
			if res.Err != nil {
				out["error"] = res.Err.Error()
			}
			line, _ := json.Marshal(out)
			fmt.Fprintf(w, "%s\n", line)
		}
	}

	if anyErr {
		return fmt.Errorf("one or more jobs failed")
	}
	return nil
}
