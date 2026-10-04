package app

import (
	"log/slog"

	"omnidatabus/internal/buildinfo"
	"omnidatabus/internal/config"
	"omnidatabus/internal/connections"
	"omnidatabus/internal/connectors/source"
	"omnidatabus/internal/connectors/target"
	"omnidatabus/internal/crypto"
	"omnidatabus/internal/discovery"
	"omnidatabus/internal/jobs"
	"omnidatabus/internal/pipeline"
	"omnidatabus/internal/store"
)

// NewWorker wires the job worker to the migration pipeline (OC-023, OC-025).
func NewWorker(cfg config.Config, st store.Store, sealer crypto.Sealer, sources *source.Registry, targets *target.Registry, logger *slog.Logger) *jobs.Worker {
	conns := connections.NewService(st, sealer, sources, targets, jobs.Guard{Store: st}, nil)
	disc := discovery.NewService(st, conns.OpenSource, nil)
	runner := pipeline.NewRunner(pipeline.Deps{
		Store: st, OpenSource: conns.OpenSource, OpenWriter: conns.OpenWriter, Latest: disc.Latest, Log: logger,
		MemoryBudgetBytes: int64(cfg.Worker.MemoryBudgetMB) << 20, MaxStoredErrors: cfg.Worker.ErrorsMaxRows,
		ErrorRetention: cfg.Worker.ErrorRetention,
		// Verification's document count (OC-030): best effort, through the kind's location browser.
		CountTarget: conns.CountDocuments,
	})
	return jobs.NewWorker(jobs.WorkerConfig{
		MaxJobs: cfg.Worker.MaxJobs, ShutdownBudget: cfg.ShutdownTimeout * 5 / 6, Version: buildinfo.Get().Version,
	}, st, runner, nil, logger)
}
