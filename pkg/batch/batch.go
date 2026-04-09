// Package batch provides a bounded-concurrency worker pool for parallel ember execution.
package batch

import (
	"context"
	"runtime"
	"sync"
	"time"

	"github.com/scttfrdmn/ember/pkg/sdk"
)

// Runner executes sdk.Jobs in parallel with bounded concurrency.
type Runner struct {
	SDK            *sdk.SDK
	MaxConcurrency int
}

// New returns a Runner backed by the given SDK.
// MaxConcurrency defaults to runtime.NumCPU() when <= 0.
func New(s *sdk.SDK) *Runner {
	return &Runner{SDK: s}
}

// Run executes all jobs in parallel up to MaxConcurrency goroutines.
// Results are returned in submission order regardless of completion order.
// Individual job failures are captured in Result.Err — Run itself always
// returns a nil error.
// Context cancellation causes pending (not-yet-started) jobs to be skipped
// with ctx.Err() as their error; in-flight jobs respect context through Burn.
func (r *Runner) Run(ctx context.Context, jobs []sdk.Job) ([]sdk.Result, error) {
	concurrency := r.MaxConcurrency
	if concurrency <= 0 {
		concurrency = runtime.NumCPU()
	}

	results := make([]sdk.Result, len(jobs))
	sem := make(chan struct{}, concurrency)

	var wg sync.WaitGroup
	for i, job := range jobs {
		i, job := i, job // capture loop variables
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Skip if context already cancelled before we acquire the semaphore.
			if ctx.Err() != nil {
				results[i] = sdk.Result{ID: job.ID, Err: ctx.Err()}
				return
			}
			sem <- struct{}{}
			defer func() { <-sem }()

			start := time.Now()
			vals, err := r.SDK.Burn(ctx, job.Artifact, job.Fn, job.Args)
			results[i] = sdk.Result{
				ID:      job.ID,
				Values:  vals,
				Err:     err,
				Elapsed: time.Since(start),
			}
		}()
	}
	wg.Wait()
	return results, nil
}
