package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// freshStatus starts a test with an empty status and no pending retries.
func freshStatus(t *testing.T) {
	t.Helper()
	status = newStatus()
	pendingRetries = nil
	t.Cleanup(func() {
		status = newStatus()
		pendingRetries = nil
	})
}

func readStatus(t *testing.T, dir string) Status {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, statusFileName))
	if err != nil {
		t.Fatal(err)
	}
	var s Status
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatalf("status.json is not valid JSON: %v\n%s", err, data)
	}
	return s
}

func TestStatus(t *testing.T) {
	quietLog(t)
	// Sunday 2024-03-10 03:00 UTC
	now := time.Date(2024, 3, 10, 3, 0, 0, 0, time.UTC)

	newConfig := func(t *testing.T, schedule BackupSchedule, retry *RetryConfig) *Config {
		t.Helper()
		freshStatus(t)
		c := &Config{BackupDirectory: t.TempDir(), Retry: retry}
		c.Backups.Daily = schedule
		return c
	}
	both := BackupSchedule{Time: "03:00", Filesystem: &StorageTarget{RetainCount: 3}, Restic: &StorageTarget{RetainCount: 3}}

	t.Run("success of both targets", func(t *testing.T) {
		fakeExec(t, "")
		c := newConfig(t, both, nil)
		handleBackup(c, "daily", now)

		s := readStatus(t, c.BackupDirectory)
		fs, restic := s.Backups["daily"]["filesystem"], s.Backups["daily"]["restic"]
		if fs == nil || fs.Result != resultSuccess || fs.Attempts != 1 || fs.LastSuccess.IsZero() || !fs.Scheduled.Equal(now) {
			t.Errorf("filesystem = %+v", fs)
		}
		if restic == nil || restic.Result != resultSuccess || restic.LastSuccessRun != "run-20240310T030000Z" {
			t.Errorf("restic = %+v", restic)
		}
		if s.Updated.IsZero() || s.Started.IsZero() {
			t.Errorf("started/updated missing: %+v", s)
		}
		if names := names(t, c.BackupDirectory); strings.Contains(strings.Join(names, ","), ".tmp") {
			t.Errorf("temporary file left behind: %v", names)
		}
	})

	t.Run("failure is retried and ends as failed, last success is kept", func(t *testing.T) {
		fakeExec(t, "")
		c := newConfig(t, BackupSchedule{Time: "03:00", RetainCount: 3}, &RetryConfig{Attempts: 2, WindowMinutes: 10})
		handleBackup(c, "daily", now.Add(-24*time.Hour)) // an earlier success
		if err := os.RemoveAll(filepath.Join(c.BackupDirectory, "daily")); err != nil {
			t.Fatal(err) // otherwise the next run is skipped because a fresh backup exists
		}
		t.Setenv("FAKE_FAIL_ON", createScript)
		handleBackup(c, "daily", now)

		fs := readStatus(t, c.BackupDirectory).Backups["daily"]["filesystem"]
		if fs.Result != resultRetrying || fs.Attempts != 1 || !fs.NextRetry.Equal(now.Add(5*time.Minute)) || fs.Detail == "" {
			t.Errorf("after first failure: %+v", fs)
		}
		if fs.LastSuccess.IsZero() {
			t.Error("last success lost after a failure")
		}
		runPendingRetries(c, now.Add(5*time.Minute))
		runPendingRetries(c, now.Add(10*time.Minute))

		fs = readStatus(t, c.BackupDirectory).Backups["daily"]["filesystem"]
		if fs.Result != resultFailed || fs.Attempts != 3 || !fs.NextRetry.IsZero() || fs.LastSuccess.IsZero() {
			t.Errorf("after the last retry: %+v", fs)
		}
	})

	t.Run("retry window exceeded", func(t *testing.T) {
		fakeExec(t, createScript)
		c := newConfig(t, BackupSchedule{Time: "03:00", RetainCount: 3}, &RetryConfig{Attempts: 3, WindowMinutes: 15})
		handleBackup(c, "daily", now)
		runPendingRetries(c, now.Add(20*time.Minute))

		fs := readStatus(t, c.BackupDirectory).Backups["daily"]["filesystem"]
		if fs.Result != resultFailed || fs.Detail != "retry window exceeded" || fs.Attempts != 1 {
			t.Errorf("filesystem = %+v", fs)
		}
	})

	t.Run("skipped for low storage", func(t *testing.T) {
		fakeExec(t, "")
		c := newConfig(t, BackupSchedule{Time: "03:00", RetainCount: 3}, nil)
		c.MinStoragePercentage = 101
		handleBackup(c, "daily", now)

		fs := readStatus(t, c.BackupDirectory).Backups["daily"]["filesystem"]
		if fs.Result != resultSkipped || fs.Detail != "not enough free storage" {
			t.Errorf("filesystem = %+v", fs)
		}
	})

	t.Run("skipped because a higher tier runs", func(t *testing.T) {
		fakeExec(t, "")
		c := newConfig(t, both, nil)
		c.Exclusive = true
		c.Backups.Weekly = BackupSchedule{Time: "03:00", DayOfWeek: "Sunday", RetainCount: 1}
		handleBackup(c, "daily", now)

		daily := readStatus(t, c.BackupDirectory).Backups["daily"]
		for _, target := range []string{"filesystem", "restic"} {
			if ts := daily[target]; ts == nil || ts.Result != resultSkipped || ts.Detail == "" {
				t.Errorf("daily %s = %+v", target, ts)
			}
		}
	})

	t.Run("contains no credentials", func(t *testing.T) {
		fakeExec(t, "")
		t.Setenv("RESTIC_REPOSITORY", "rest:https://user:topsecret@backup.example/repo")
		t.Setenv("RESTIC_PASSWORD", "topsecret")
		c := newConfig(t, BackupSchedule{Time: "03:00", Restic: &StorageTarget{RetainCount: 3}}, nil)
		handleBackup(c, "daily", now)

		data, err := os.ReadFile(filepath.Join(c.BackupDirectory, statusFileName))
		if err != nil || strings.Contains(string(data), "topsecret") || strings.Contains(string(data), "backup.example") {
			t.Errorf("err = %v, status.json = %s", err, data)
		}
	})
}

func TestLoadStatus(t *testing.T) {
	quietLog(t)

	t.Run("pending retries do not survive a restart", func(t *testing.T) {
		freshStatus(t)
		dir := t.TempDir()
		previous := `{"backups":{"daily":{"filesystem":{"result":"retrying","attempts":1,` +
			`"nextRetry":"2024-03-10T03:05:00Z","lastAttempt":"2024-03-10T03:00:00Z",` +
			`"lastSuccess":"2024-03-09T03:00:00Z"}}}}`
		if err := os.WriteFile(filepath.Join(dir, statusFileName), []byte(previous), 0644); err != nil {
			t.Fatal(err)
		}
		c := &Config{BackupDirectory: dir}
		loadStatus(c)
		writeStatus(c)

		fs := readStatus(t, dir).Backups["daily"]["filesystem"]
		if fs.Result != resultFailed || !strings.Contains(fs.Detail, "restart") || !fs.NextRetry.IsZero() || fs.LastSuccess.IsZero() {
			t.Errorf("filesystem = %+v", fs)
		}
	})

	t.Run("unreadable file is ignored", func(t *testing.T) {
		freshStatus(t)
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, statusFileName), []byte("{not json"), 0644); err != nil {
			t.Fatal(err)
		}
		loadStatus(&Config{BackupDirectory: dir})
		if len(status.Backups) != 0 {
			t.Errorf("status = %+v", status)
		}
	})

	t.Run("no file is fine", func(t *testing.T) {
		freshStatus(t)
		loadStatus(&Config{BackupDirectory: t.TempDir()})
	})
}

func TestRunStatusCommand(t *testing.T) {
	var out, errOut bytes.Buffer

	if rc := runStatusCommand(nil, t.TempDir(), &out, &errOut); rc != 1 || !strings.Contains(errOut.String(), "no status") {
		t.Errorf("without a status file: rc = %d, stderr = %q", rc, errOut.String())
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, statusFileName), []byte(`{"backups":{}}`+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if rc := runStatusCommand(nil, dir, &out, &errOut); rc != 0 || out.String() != "{\"backups\":{}}\n" {
		t.Errorf("rc = %d, stdout = %q", rc, out.String())
	}

	for args, want := range map[string]int{"--help": 0, "bogus": 1} {
		errOut.Reset()
		if rc := runStatusCommand([]string{args}, dir, &out, &errOut); rc != want || !strings.Contains(errOut.String(), "usage") {
			t.Errorf("args %q: rc = %d, stderr = %q", args, rc, errOut.String())
		}
	}
}
