package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"

	"adaptive-vocabulary/internal/backup"
	"adaptive-vocabulary/internal/core"
	"adaptive-vocabulary/internal/learning"
	"adaptive-vocabulary/internal/maintenance"
	"adaptive-vocabulary/internal/platform"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	cmd := os.Args[1]
	switch cmd {
	case "serve":
		runServe(os.Args[2:])
	case "doctor":
		runDoctor(os.Args[2:])
	case "version":
		fmt.Println("avocab version 0.1.0 (contract 1.1.0)")
	case "learning":
		if len(os.Args) > 2 && os.Args[2] == "rebuild" {
			runLearningRebuild(os.Args[3:])
			return
		}
		if runExtraCommands(cmd, os.Args[2:]) {
			return
		}
		fmt.Fprintf(os.Stderr, "unknown learning command: %v\n", os.Args[2:])
		os.Exit(1)
	case "backup":
		runBackup(os.Args[2:])
	default:
		if runExtraCommands(cmd, os.Args[2:]) {
			return
		}
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", cmd)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println("Usage: avocab <command> [flags]")
	fmt.Println("Commands:")
	fmt.Println("  serve   Start the Adaptive Vocabulary HTTP server")
	fmt.Println("  doctor  Verify system environment and data directory state")
	fmt.Println("  backup  Create, verify, and restore full backups")
	fmt.Println("  version Display version information")
}

func runServe(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	dataDir := fs.String("data-dir", "data/local", "Path to data directory")
	listen := fs.String("listen", "127.0.0.1:8080", "Listen address (loopback 127.0.0.1 only)")
	policy := fs.String("policy", "config/policy-v2.json", "Path to policy JSON file")
	seed := fs.String("seed", "", "Optional seed dataset (e.g. core)")
	_ = fs.Parse(args)

	cfg, err := platform.ValidateAndResolveConfig(*dataDir, *listen, *policy, *seed)
	if err != nil {
		fmt.Fprintf(os.Stderr, "configuration error: %v\n", err)
		os.Exit(1)
	}

	server, err := platform.NewServer(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "server init error: %v\n", err)
		os.Exit(1)
	}

	if err := server.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "server runtime error: %v\n", err)
		os.Exit(1)
	}
}

func runDoctor(args []string) {
	fs := flag.NewFlagSet("doctor", flag.ExitOnError)
	dataDir := fs.String("data-dir", "data/local", "Path to data directory")
	policy := fs.String("policy", "config/policy-v2.json", "Path to policy JSON file")
	_ = fs.Parse(args)

	cfg, err := platform.ValidateAndResolveConfig(*dataDir, "127.0.0.1:0", *policy, "")
	if err != nil {
		fmt.Fprintf(os.Stderr, "doctor configuration error: %v\n", err)
		os.Exit(1)
	}

	// Try acquiring process lock
	lock, err := platform.AcquireProcessLock(cfg.DataDir)
	if err != nil {
		report := map[string]any{
			"status":   "error",
			"code":     "SERVICE_RUNNING",
			"message":  err.Error(),
			"data_dir": cfg.DataDir,
		}
		_ = json.NewEncoder(os.Stdout).Encode(report)
		os.Exit(2)
	}
	defer func() { _ = lock.Release() }()

	db, err := platform.OpenDB(cfg.DataDir)
	if err != nil {
		report := map[string]any{
			"status":  "error",
			"message": fmt.Sprintf("database open error: %v", err),
		}
		_ = json.NewEncoder(os.Stdout).Encode(report)
		os.Exit(1)
	}
	defer func() { _ = db.Close() }()

	schemaVersion, err := platform.RunMigrations(db.DB)
	if err != nil {
		report := map[string]any{
			"status":  "error",
			"message": fmt.Sprintf("migration error: %v", err),
		}
		_ = json.NewEncoder(os.Stdout).Encode(report)
		os.Exit(1)
	}

	report := map[string]any{
		"status":           "ok",
		"contract_version": cfg.ContractVersion,
		"schema_version":   schemaVersion,
		"sqlite_version":   db.Version,
		"sqlite_source_id": db.SourceID,
		"policy_version":   cfg.Policy.DesignVersion,
		"data_dir":         cfg.DataDir,
	}
	_ = json.NewEncoder(os.Stdout).Encode(report)
}

func runLearningRebuild(args []string) {
	if len(args) == 0 {
		fmt.Fprintf(os.Stderr, "Usage: avocab learning rebuild <resume|abort|restart> [flags]\n")
		os.Exit(1)
	}
	action := args[0]
	actionArgs := args[1:]

	fs := flag.NewFlagSet("learning rebuild "+action, flag.ExitOnError)
	runID := fs.String("run", "", "Rebuild run ID")
	projection := fs.String("projection", "", "Target projection version for restart")
	dataDir := fs.String("data-dir", "data/local", "Path to data directory")
	policy := fs.String("policy", "config/policy-v2.json", "Path to policy JSON file")
	_ = fs.Parse(actionArgs)

	if *runID == "" {
		fmt.Fprintf(os.Stderr, "--run is required for learning rebuild %s\n", action)
		os.Exit(1)
	}

	cfg, err := platform.ValidateAndResolveConfig(*dataDir, "127.0.0.1:0", *policy, "")
	if err != nil {
		fmt.Fprintf(os.Stderr, "configuration error: %v\n", err)
		os.Exit(1)
	}

	lock, err := platform.AcquireProcessLock(cfg.DataDir)
	if err != nil {
		report := map[string]any{
			"status":   "error",
			"code":     "SERVICE_RUNNING",
			"message":  err.Error(),
			"data_dir": cfg.DataDir,
		}
		_ = json.NewEncoder(os.Stdout).Encode(report)
		os.Exit(2)
	}
	defer func() { _ = lock.Release() }()

	db, err := platform.OpenDB(cfg.DataDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to open DB: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = db.Close() }()

	var fsrsPolicy learning.FSRSPolicyConfig
	if p, err := learning.LoadFSRSPolicyFromPath(cfg.PolicyPath); err == nil && p != nil {
		fsrsPolicy = *p
	}
	maintSvc := maintenance.NewServiceWithPolicy(db.DB, fsrsPolicy)

	var res *maintenance.RebuildResult
	switch action {
	case "resume":
		res, err = maintSvc.Resume(context.Background(), *runID)
	case "abort":
		res, err = maintSvc.Abort(context.Background(), *runID)
	case "restart":
		if *projection == "" {
			fmt.Fprintf(os.Stderr, "--projection is required for learning rebuild restart\n")
			os.Exit(1)
		}
		res, err = maintSvc.Restart(context.Background(), *runID, *projection)
	default:
		fmt.Fprintf(os.Stderr, "unknown rebuild action: %s\n", action)
		os.Exit(1)
	}

	if err != nil {
		if errors.Is(err, maintenance.ErrRebuildConflict) || errors.Is(err, maintenance.ErrPredecessorConflict) {
			report := map[string]any{
				"status":  "error",
				"code":    "REBUILD_STATE_CONFLICT",
				"message": err.Error(),
			}
			_ = json.NewEncoder(os.Stdout).Encode(report)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "rebuild %s error: %v\n", action, err)
		os.Exit(1)
	}

	_ = json.NewEncoder(os.Stdout).Encode(res)
}

func runBackup(args []string) {
	if len(args) == 0 {
		fmt.Fprintf(os.Stderr, "Usage: avocab backup <create|verify|restore> [flags]\n")
		os.Exit(1)
	}

	action := args[0]
	actionArgs := args[1:]

	svc := backup.NewService()
	ctx := context.Background()

	switch action {
	case "create":
		fs := flag.NewFlagSet("backup create", flag.ExitOnError)
		outputDir := fs.String("output", "", "Output directory for backup (required)")
		dataDir := fs.String("data-dir", "data/local", "Path to data directory")
		includeCheckpoints := fs.Bool("include-checkpoints", false, "Include generation checkpoint database if present")
		_ = fs.Parse(actionArgs)

		if *outputDir == "" {
			fmt.Fprintf(os.Stderr, "--output is required for backup create\n")
			os.Exit(1)
		}

		manifest, err := svc.CreateBackup(ctx, *dataDir, *outputDir, *includeCheckpoints)
		if err != nil {
			report := map[string]any{
				"status":  "failed",
				"message": fmt.Sprintf("backup create failed: %v", err),
			}
			_ = json.NewEncoder(os.Stdout).Encode(report)
			os.Exit(1)
		}

		report := map[string]any{
			"status":           "ok",
			"operation_id":     core.NewUUID(),
			"output_directory": *outputDir,
			"manifest":         manifest,
			"event_watermark":  manifest.SnapshotWatermark,
		}
		_ = json.NewEncoder(os.Stdout).Encode(report)

	case "verify":
		fs := flag.NewFlagSet("backup verify", flag.ExitOnError)
		inputDir := fs.String("input", "", "Path to backup directory to verify (required)")
		_ = fs.Parse(actionArgs)

		if *inputDir == "" {
			fmt.Fprintf(os.Stderr, "--input is required for backup verify\n")
			os.Exit(1)
		}

		rep, err := svc.VerifyBackup(ctx, *inputDir)
		_ = json.NewEncoder(os.Stdout).Encode(rep)
		if err != nil || rep.Status != "ok" {
			os.Exit(1)
		}

	case "restore":
		fs := flag.NewFlagSet("backup restore", flag.ExitOnError)
		inputDir := fs.String("input", "", "Path to backup directory to restore from (required)")
		targetDir := fs.String("target", "", "Target directory for restored data (required)")
		_ = fs.Parse(actionArgs)

		if *inputDir == "" || *targetDir == "" {
			fmt.Fprintf(os.Stderr, "--input and --target are required for backup restore\n")
			os.Exit(1)
		}

		rep, err := svc.RestoreBackup(ctx, *inputDir, *targetDir)
		if errors.Is(err, backup.ErrActiveTargetDirectory) {
			_ = json.NewEncoder(os.Stdout).Encode(map[string]any{
				"status":           "error",
				"code":             "SERVICE_RUNNING",
				"message":          "target directory has an active running service or locked process",
				"target_directory": *targetDir,
			})
			os.Exit(2)
		}

		_ = json.NewEncoder(os.Stdout).Encode(rep)
		if err != nil || rep.Status != "ok" {
			os.Exit(1)
		}

	default:
		fmt.Fprintf(os.Stderr, "unknown backup action: %s\n", action)
		os.Exit(1)
	}
}
