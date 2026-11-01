package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"time"
)

// The service records the outcome of every backup attempt in <backupDirectory>/status.json, so that
// operators can see whether the backups work without parsing the log. The file is replaced atomically
// and never contains credentials (no repository URL, no passwords).

const statusFileName = "status.json"

const (
	resultSuccess  = "success"
	resultFailed   = "failed"   // final: no retry left
	resultRetrying = "retrying" // the last attempt failed, the next one is scheduled
	resultSkipped  = "skipped"  // not a failure, see detail
)

// TargetStatus is the state of one storage target (filesystem or restic) of one tier.
type TargetStatus struct {
	Result         string    `json:"result"`
	Detail         string    `json:"detail,omitempty"` // error message or reason for a skip
	Scheduled      time.Time `json:"scheduled,omitzero"`
	LastAttempt    time.Time `json:"lastAttempt"`
	Attempts       int       `json:"attempts,omitempty"` // attempts made for the scheduled run
	NextRetry      time.Time `json:"nextRetry,omitzero"`
	LastSuccess    time.Time `json:"lastSuccess,omitzero"`
	LastSuccessRun string    `json:"lastSuccessRun,omitempty"` // restic: run tag of the last success
}

// Status is the content of status.json.
type Status struct {
	Version string                              `json:"version,omitempty"`
	Started time.Time                           `json:"started"`
	Updated time.Time                           `json:"updated"`
	Backups map[string]map[string]*TargetStatus `json:"backups"` // tier -> target -> state
}

// status is only touched from the main loop.
var status = newStatus()

func newStatus() *Status {
	return &Status{
		Version: os.Getenv("MEDIAWIKI_BACKUP_VERSION"),
		Started: time.Now(),
		Backups: map[string]map[string]*TargetStatus{},
	}
}

func statusPath(backupDirectory string) string {
	if backupDirectory == "" {
		backupDirectory = "/backup"
	}
	return filepath.Join(backupDirectory, statusFileName)
}

// recordStatus updates the state of one target and writes status.json.
func recordStatus(config *Config, tier, target string, update func(*TargetStatus)) {
	targets := status.Backups[tier]
	if targets == nil {
		targets = map[string]*TargetStatus{}
		status.Backups[tier] = targets
	}
	ts := targets[target]
	if ts == nil {
		ts = &TargetStatus{}
		targets[target] = ts
	}
	ts.LastAttempt = time.Now()
	update(ts)
	writeStatus(config)
}

// writeStatus replaces status.json atomically. Without a backup directory (unit tests) it does nothing.
func writeStatus(config *Config) {
	if config.BackupDirectory == "" {
		return
	}
	status.Updated = time.Now()
	data, err := json.MarshalIndent(status, "", "  ")
	if err != nil {
		log.Printf("Error encoding %s: %v", statusFileName, err)
		return
	}
	path := statusPath(config.BackupDirectory)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0644); err != nil {
		log.Printf("Error writing %s: %v", statusFileName, err)
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		log.Printf("Error replacing %s: %v", statusFileName, err)
		_ = os.Remove(tmp)
	}
}

// loadStatus takes over the state of a previous run of the service. Retries are only kept in memory, so
// those that were still pending are reported as failed.
func loadStatus(config *Config) {
	data, err := os.ReadFile(statusPath(config.BackupDirectory))
	if err != nil {
		return
	}
	var previous Status
	if err := json.Unmarshal(data, &previous); err != nil {
		log.Printf("Ignoring unreadable %s: %v", statusFileName, err)
		return
	}
	if previous.Backups == nil {
		previous.Backups = map[string]map[string]*TargetStatus{}
	}
	for _, targets := range previous.Backups {
		for _, ts := range targets {
			if ts != nil && ts.Result == resultRetrying {
				ts.Result = resultFailed
				ts.Detail = "interrupted by a restart of the service"
				ts.NextRetry = time.Time{}
			}
		}
	}
	previous.Version = status.Version
	previous.Started = status.Started
	status = &previous
}

// runStatusCommand implements "docker-mediawiki-backup status": it prints status.json.
func runStatusCommand(args []string, backupDirectory string, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		fmt.Fprintln(stderr, "usage: docker-mediawiki-backup status")
		fmt.Fprintln(stderr, "  print the outcome of the last backup runs (status.json in the backup directory)")
		if args[0] == "-h" || args[0] == "--help" {
			return 0
		}
		return 1
	}
	data, err := os.ReadFile(statusPath(backupDirectory))
	if err != nil {
		fmt.Fprintf(stderr, "no status available: %v\n", err)
		return 1
	}
	_, _ = stdout.Write(data)
	return 0
}
