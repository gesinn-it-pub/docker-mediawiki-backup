package main

import (
	"bytes"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ---- helpers ----

func quietLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	return &buf
}

func writeFile(t *testing.T, path string, mod time.Time) {
	t.Helper()
	if err := os.WriteFile(path, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mod, mod); err != nil {
		t.Fatal(err)
	}
}

func names(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

// fakeExec replaces execCommand with a re-exec of the test binary (see
// TestHelperProcess). Every invocation is appended to the returned record file
// as "<name> <args...> | BACKUP_FILE=<value>". Invocations whose command line
// contains failOn exit non-zero.
func fakeExec(t *testing.T, failOn string) (record string) {
	t.Helper()
	record = filepath.Join(t.TempDir(), "calls.txt")
	t.Setenv("GO_WANT_HELPER_PROCESS", "1")
	t.Setenv("FAKE_RECORD", record)
	t.Setenv("FAKE_FAIL_ON", failOn)

	orig := execCommand
	execCommand = func(name string, args ...string) *exec.Cmd {
		a := append([]string{"-test.run=TestHelperProcess", "--", name}, args...)
		return exec.Command(os.Args[0], a...)
	}
	t.Cleanup(func() { execCommand = orig })
	return record
}

func calls(t *testing.T, record string) []string {
	t.Helper()
	data, err := os.ReadFile(record)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}

func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	args := os.Args
	for i, a := range args {
		if a == "--" {
			args = args[i+1:]
			break
		}
	}
	line := strings.Join(args, " ")
	f, err := os.OpenFile(os.Getenv("FAKE_RECORD"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		os.Exit(2)
	}
	f.WriteString(line + " | BACKUP_FILE=" + os.Getenv("BACKUP_FILE") + "\n")
	f.Close()
	if failOn := os.Getenv("FAKE_FAIL_ON"); failOn != "" && strings.Contains(line, failOn) {
		os.Stderr.WriteString("simulated failure\n")
		os.Exit(1)
	}
	os.Exit(0)
}

// ---- config ----

func TestFilesystemRetainCount(t *testing.T) {
	tests := []struct {
		name  string
		sched BackupSchedule
		count int
		ok    bool
	}{
		{"nothing configured", BackupSchedule{}, 0, false},
		{"legacy retainCount", BackupSchedule{RetainCount: 7}, 7, true},
		{"explicit filesystem", BackupSchedule{Filesystem: &StorageTarget{RetainCount: 3}}, 3, true},
		{"filesystem wins over legacy", BackupSchedule{RetainCount: 7, Filesystem: &StorageTarget{RetainCount: 3}}, 3, true},
		{"explicit filesystem with zero", BackupSchedule{Filesystem: &StorageTarget{}}, 0, true},
		{"restic only", BackupSchedule{Restic: &StorageTarget{RetainCount: 5}}, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			count, ok := tt.sched.filesystemRetainCount()
			if count != tt.count || ok != tt.ok {
				t.Errorf("got (%d, %v), want (%d, %v)", count, ok, tt.count, tt.ok)
			}
		})
	}
}

func TestLoadConfig(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
		return p
	}

	t.Run("full config", func(t *testing.T) {
		p := write("full.json", `{
			"backupDirectory": "/data",
			"minStoragePercentage": 15.5,
			"backups": {
				"daily":   {"time": "03:00", "retainCount": 7},
				"weekly":  {"time": "04:00", "dayOfWeek": "Sunday", "filesystem": {"retainCount": 4}, "restic": {"retainCount": 8}},
				"monthly": {"time": "05:00", "dayOfMonth": 1}
			}
		}`)
		c, err := loadConfig(p)
		if err != nil {
			t.Fatal(err)
		}
		if c.BackupDirectory != "/data" || c.MinStoragePercentage != 15.5 {
			t.Errorf("unexpected top-level values: %+v", c)
		}
		if c.Backups.Daily.RetainCount != 7 || c.Backups.Daily.Time != "03:00" {
			t.Errorf("daily: %+v", c.Backups.Daily)
		}
		w := c.Backups.Weekly
		if w.DayOfWeek != "Sunday" || w.Filesystem.RetainCount != 4 || w.Restic.RetainCount != 8 {
			t.Errorf("weekly: %+v", w)
		}
		if c.Backups.Monthly.DayOfMonth != 1 || c.Backups.Monthly.Restic != nil {
			t.Errorf("monthly: %+v", c.Backups.Monthly)
		}
	})

	t.Run("empty object", func(t *testing.T) {
		c, err := loadConfig(write("empty.json", `{}`))
		if err != nil {
			t.Fatal(err)
		}
		if c.BackupDirectory != "" || c.MinStoragePercentage != 0 {
			t.Errorf("expected zero values, got %+v", c)
		}
	})

	t.Run("missing file", func(t *testing.T) {
		if _, err := loadConfig(filepath.Join(dir, "nope.json")); err == nil {
			t.Error("expected error")
		}
	})

	t.Run("invalid json", func(t *testing.T) {
		if _, err := loadConfig(write("bad.json", `{not json`)); err == nil {
			t.Error("expected error")
		}
	})
}

func TestApplyDefaults(t *testing.T) {
	quietLog(t)

	c := &Config{}
	applyDefaults(c)
	if c.BackupDirectory != "/backup" || c.MinStoragePercentage != 20.0 {
		t.Errorf("defaults not applied: %+v", c)
	}

	c = &Config{BackupDirectory: "/x", MinStoragePercentage: 5}
	applyDefaults(c)
	if c.BackupDirectory != "/x" || c.MinStoragePercentage != 5 {
		t.Errorf("explicit values overwritten: %+v", c)
	}
}

func TestGetSchedule(t *testing.T) {
	c := &Config{}
	c.Backups.Daily.Time = "d"
	c.Backups.Weekly.Time = "w"
	c.Backups.Monthly.Time = "m"

	for typ, want := range map[string]string{"daily": "d", "weekly": "w", "monthly": "m", "yearly": ""} {
		if got := getSchedule(c, typ).Time; got != want {
			t.Errorf("getSchedule(%q).Time = %q, want %q", typ, got, want)
		}
	}
}

// ---- scheduling ----

func TestShouldRunBackup(t *testing.T) {
	// 2024-03-10 is a Sunday.
	at := func(h, m int) time.Time { return time.Date(2024, 3, 10, h, m, 30, 0, time.UTC) }

	tests := []struct {
		name  string
		typ   string
		sched BackupSchedule
		now   time.Time
		want  bool
	}{
		{"daily at time", "daily", BackupSchedule{Time: "03:00"}, at(3, 0), true},
		{"daily wrong minute", "daily", BackupSchedule{Time: "03:00"}, at(3, 1), false},
		{"daily wrong hour", "daily", BackupSchedule{Time: "03:00"}, at(4, 0), false},
		{"weekly right day", "weekly", BackupSchedule{Time: "03:00", DayOfWeek: "Sunday"}, at(3, 0), true},
		{"weekly wrong day", "weekly", BackupSchedule{Time: "03:00", DayOfWeek: "Monday"}, at(3, 0), false},
		{"weekly right day wrong time", "weekly", BackupSchedule{Time: "03:00", DayOfWeek: "Sunday"}, at(2, 59), false},
		{"weekly unset day", "weekly", BackupSchedule{Time: "03:00"}, at(3, 0), false},
		{"monthly right day", "monthly", BackupSchedule{Time: "03:00", DayOfMonth: 10}, at(3, 0), true},
		{"monthly wrong day", "monthly", BackupSchedule{Time: "03:00", DayOfMonth: 11}, at(3, 0), false},
		{"monthly unset day", "monthly", BackupSchedule{Time: "03:00"}, at(3, 0), false},
		{"unknown type", "yearly", BackupSchedule{Time: "03:00"}, at(3, 0), false},
		// Characterization: an unparsable/missing time falls back to 00:00.
		{"daily without time runs at midnight", "daily", BackupSchedule{}, at(0, 0), true},
		{"daily without time not at noon", "daily", BackupSchedule{}, at(12, 0), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldRunBackup(tt.sched, tt.typ, tt.now); got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

// ---- filesystem ----

func TestCheckStorage(t *testing.T) {
	quietLog(t)
	dir := t.TempDir()

	if !CheckStorage(dir, 0) {
		t.Error("0% required must always pass")
	}
	if CheckStorage(dir, 101) {
		t.Error("101% required must never pass")
	}
	if CheckStorage(filepath.Join(dir, "missing"), 0) {
		t.Error("nonexistent directory must fail")
	}
}

func TestEnsureBackupPath(t *testing.T) {
	quietLog(t)
	base := t.TempDir()

	p := filepath.Join(base, "a", "b")
	got, err := ensureBackupPath(p)
	if err != nil || got != p {
		t.Fatalf("got (%q, %v)", got, err)
	}
	if st, err := os.Stat(p); err != nil || !st.IsDir() {
		t.Errorf("directory not created: %v", err)
	}
	if _, err := ensureBackupPath(p); err != nil {
		t.Errorf("existing directory must not error: %v", err)
	}

	blocker := filepath.Join(base, "file")
	writeFile(t, blocker, time.Now())
	if _, err := ensureBackupPath(filepath.Join(blocker, "sub")); err == nil {
		t.Error("expected error when a parent is a regular file")
	}
}

func TestGetLastBackupTime(t *testing.T) {
	dir := t.TempDir()

	got, err := getLastBackupTime(dir)
	if err != nil || !got.IsZero() {
		t.Errorf("empty dir: got (%v, %v), want zero time", got, err)
	}

	old := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	newest := time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC)
	writeFile(t, filepath.Join(dir, "old"), old)
	writeFile(t, filepath.Join(dir, "new"), newest)
	writeFile(t, filepath.Join(dir, "mid"), old.Add(24*time.Hour))

	got, err = getLastBackupTime(dir)
	if err != nil || !got.Equal(newest) {
		t.Errorf("got (%v, %v), want %v", got, err, newest)
	}

	if _, err := getLastBackupTime(filepath.Join(dir, "missing")); err == nil {
		t.Error("expected error for missing directory")
	}
}

func TestCleanupOldBackups(t *testing.T) {
	quietLog(t)
	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	setup := func(t *testing.T) string {
		dir := t.TempDir()
		// Names deliberately do not sort like their mtimes.
		writeFile(t, filepath.Join(dir, "c"), base)
		writeFile(t, filepath.Join(dir, "a"), base.Add(3*time.Hour))
		writeFile(t, filepath.Join(dir, "d"), base.Add(1*time.Hour))
		writeFile(t, filepath.Join(dir, "b"), base.Add(2*time.Hour))
		return dir
	}

	t.Run("keeps newest N", func(t *testing.T) {
		dir := setup(t)
		cleanupOldBackups(dir, 2)
		got := strings.Join(names(t, dir), ",")
		if got != "a,b" { // ReadDir is name-sorted; a and b are the two newest
			t.Errorf("remaining %q, want a,b", got)
		}
	})

	t.Run("retain more than present keeps all", func(t *testing.T) {
		dir := setup(t)
		cleanupOldBackups(dir, 10)
		if n := len(names(t, dir)); n != 4 {
			t.Errorf("remaining %d, want 4", n)
		}
	})

	t.Run("retain zero removes all", func(t *testing.T) {
		dir := setup(t)
		cleanupOldBackups(dir, 0)
		if n := len(names(t, dir)); n != 0 {
			t.Errorf("remaining %d, want 0", n)
		}
	})

	t.Run("missing directory does not panic", func(t *testing.T) {
		cleanupOldBackups(filepath.Join(t.TempDir(), "missing"), 1)
	})
}

// ---- external commands ----

func TestRunBackupScript(t *testing.T) {
	quietLog(t)

	t.Run("passes BACKUP_FILE", func(t *testing.T) {
		rec := fakeExec(t, "")
		if err := runBackupScript("/backup/daily/x.tar"); err != nil {
			t.Fatal(err)
		}
		got := calls(t, rec)
		want := createScript + " | BACKUP_FILE=/backup/daily/x.tar"
		if len(got) != 1 || got[0] != want {
			t.Errorf("calls = %q, want [%q]", got, want)
		}
	})

	t.Run("propagates failure", func(t *testing.T) {
		fakeExec(t, createScript)
		if err := runBackupScript("/x.tar"); err == nil {
			t.Error("expected error")
		}
	})
}

func TestRunResticBackup(t *testing.T) {
	quietLog(t)

	t.Run("runs backup, db dump and forget in order", func(t *testing.T) {
		rec := fakeExec(t, "")
		if err := runResticBackup("weekly", 4); err != nil {
			t.Fatal(err)
		}
		got := calls(t, rec)
		if len(got) != 3 {
			t.Fatalf("got %d calls, want 3: %q", len(got), got)
		}
		wantPrefix := []string{
			"restic backup --tag weekly /var/www/html/images",
			"bash -c ",
			"restic forget --prune --tag weekly --keep-weekly 4",
		}
		for i, p := range wantPrefix {
			if !strings.HasPrefix(got[i], p) {
				t.Errorf("call %d = %q, want prefix %q", i, got[i], p)
			}
		}
		if !strings.Contains(got[1], "--tag weekly") || !strings.Contains(got[1], "mysqldump") {
			t.Errorf("db call missing tag/mysqldump: %q", got[1])
		}
	})

	failures := []struct {
		name   string
		failOn string
		calls  int // calls made before aborting, including the failing one
	}{
		{"backup fails", "restic backup --tag", 1},
		{"db dump fails", "mysqldump", 2},
		{"forget fails", "restic forget", 3},
	}
	for _, f := range failures {
		t.Run(f.name, func(t *testing.T) {
			rec := fakeExec(t, f.failOn)
			if err := runResticBackup("daily", 7); err == nil {
				t.Fatal("expected error")
			}
			if n := len(calls(t, rec)); n != f.calls {
				t.Errorf("made %d calls, want %d", n, f.calls)
			}
		})
	}
}

// ---- handleBackup ----

func TestHandleBackup(t *testing.T) {
	quietLog(t)
	// Sunday 2024-03-10 03:00 UTC
	now := time.Date(2024, 3, 10, 3, 0, 0, 0, time.UTC)

	newConfig := func(t *testing.T, daily BackupSchedule) *Config {
		c := &Config{BackupDirectory: t.TempDir()}
		c.Backups.Daily = daily
		return c
	}

	t.Run("not due does nothing", func(t *testing.T) {
		rec := fakeExec(t, "")
		c := newConfig(t, BackupSchedule{Time: "04:00", RetainCount: 3})
		handleBackup(c, "daily", now)
		if got := calls(t, rec); got != nil {
			t.Errorf("unexpected calls: %q", got)
		}
		if _, err := os.Stat(filepath.Join(c.BackupDirectory, "daily")); !os.IsNotExist(err) {
			t.Error("backup directory must not be created when not due")
		}
	})

	t.Run("due filesystem backup", func(t *testing.T) {
		rec := fakeExec(t, "")
		c := newConfig(t, BackupSchedule{Time: "03:00", RetainCount: 3})
		handleBackup(c, "daily", now)

		wantFile := filepath.Join(c.BackupDirectory, "daily", "mediawiki-backup-daily-10.03.2024.tar")
		got := calls(t, rec)
		if len(got) != 1 || got[0] != createScript+" | BACKUP_FILE="+wantFile {
			t.Errorf("calls = %q", got)
		}
	})

	t.Run("failed script does not clean up", func(t *testing.T) {
		fakeExec(t, createScript)
		c := newConfig(t, BackupSchedule{Time: "03:00", RetainCount: 1})
		dir := filepath.Join(c.BackupDirectory, "daily")
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(dir, "old1"), now.Add(-72*time.Hour))
		writeFile(t, filepath.Join(dir, "old2"), now.Add(-48*time.Hour))

		handleBackup(c, "daily", now)
		if n := len(names(t, dir)); n != 2 {
			t.Errorf("%d files left, want 2 (no cleanup after failed backup)", n)
		}
	})

	t.Run("successful backup cleans up old ones", func(t *testing.T) {
		fakeExec(t, "")
		c := newConfig(t, BackupSchedule{Time: "03:00", RetainCount: 1})
		dir := filepath.Join(c.BackupDirectory, "daily")
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(dir, "old1"), now.Add(-72*time.Hour))
		writeFile(t, filepath.Join(dir, "old2"), now.Add(-48*time.Hour))

		handleBackup(c, "daily", now)
		// The fake script creates no file, so retain=1 leaves only the newest old one.
		if got := strings.Join(names(t, dir), ","); got != "old2" {
			t.Errorf("remaining %q, want old2", got)
		}
	})

	t.Run("skipped when a backup was just made", func(t *testing.T) {
		rec := fakeExec(t, "")
		c := newConfig(t, BackupSchedule{Time: "03:00", RetainCount: 3})
		dir := filepath.Join(c.BackupDirectory, "daily")
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(dir, "recent"), now.Add(-10*time.Second))

		handleBackup(c, "daily", now)
		if got := calls(t, rec); got != nil {
			t.Errorf("unexpected calls: %q", got)
		}
	})

	t.Run("skipped when storage is insufficient", func(t *testing.T) {
		rec := fakeExec(t, "")
		c := newConfig(t, BackupSchedule{Time: "03:00", RetainCount: 3})
		c.MinStoragePercentage = 101
		handleBackup(c, "daily", now)
		if got := calls(t, rec); got != nil {
			t.Errorf("unexpected calls: %q", got)
		}
	})

	t.Run("restic only", func(t *testing.T) {
		rec := fakeExec(t, "")
		c := newConfig(t, BackupSchedule{Time: "03:00", Restic: &StorageTarget{RetainCount: 5}})
		handleBackup(c, "daily", now)

		got := calls(t, rec)
		if len(got) != 3 || !strings.HasPrefix(got[0], "restic backup") {
			t.Errorf("calls = %q", got)
		}
		if _, err := os.Stat(filepath.Join(c.BackupDirectory, "daily")); !os.IsNotExist(err) {
			t.Error("no filesystem directory expected for restic-only schedule")
		}
	})

	t.Run("filesystem and restic", func(t *testing.T) {
		rec := fakeExec(t, "")
		c := newConfig(t, BackupSchedule{
			Time:       "03:00",
			Filesystem: &StorageTarget{RetainCount: 2},
			Restic:     &StorageTarget{RetainCount: 5},
		})
		handleBackup(c, "daily", now)

		got := calls(t, rec)
		if len(got) != 4 || !strings.HasPrefix(got[0], createScript) || !strings.HasPrefix(got[1], "restic backup") {
			t.Errorf("calls = %q", got)
		}
	})

	t.Run("restic failure does not panic", func(t *testing.T) {
		fakeExec(t, "restic")
		c := newConfig(t, BackupSchedule{Time: "03:00", Restic: &StorageTarget{RetainCount: 5}})
		handleBackup(c, "daily", now)
	})
}

func TestRunDueBackups(t *testing.T) {
	quietLog(t)
	rec := fakeExec(t, "")

	// Sunday 2024-03-10 03:00: daily and weekly are due, monthly (day 1) is not.
	now := time.Date(2024, 3, 10, 3, 0, 0, 0, time.UTC)
	c := &Config{BackupDirectory: t.TempDir()}
	c.Backups.Daily = BackupSchedule{Time: "03:00", RetainCount: 1}
	c.Backups.Weekly = BackupSchedule{Time: "03:00", DayOfWeek: "Sunday", RetainCount: 1}
	c.Backups.Monthly = BackupSchedule{Time: "03:00", DayOfMonth: 1, RetainCount: 1}

	runDueBackups(c, now)

	got := calls(t, rec)
	if len(got) != 2 {
		t.Fatalf("got %d calls, want 2: %q", len(got), got)
	}
	if !strings.Contains(got[0], "mediawiki-backup-daily-") || !strings.Contains(got[1], "mediawiki-backup-weekly-") {
		t.Errorf("calls = %q", got)
	}
}

// ---- daysOfWeek / exclusive ----

func TestDueBackups(t *testing.T) {
	// 2024-03: Fri 1st (monthly day), Sat 2nd, Sun 3rd, Mon 4th, Tue 5th, Wed 6th, Sat 9th, Sat 30th
	at := func(day, h, m int) time.Time { return time.Date(2024, 3, day, h, m, 0, 0, time.UTC) }
	fs := &StorageTarget{RetainCount: 1}

	// Target scheme: daily Tue-Fri at 01:15, weekly Saturday 02:15, monthly on the 1st at 03:15.
	scheme := func(exclusive bool) *Config {
		c := &Config{Exclusive: exclusive}
		c.Backups.Daily = BackupSchedule{Time: "01:15", DaysOfWeek: []string{"Tuesday", "Wednesday", "Thursday", "Friday"}, Filesystem: fs}
		c.Backups.Weekly = BackupSchedule{Time: "02:15", DayOfWeek: "Saturday", Filesystem: fs}
		c.Backups.Monthly = BackupSchedule{Time: "03:15", DayOfMonth: 1, Filesystem: fs}
		return c
	}
	// Every tier at the same time, to exercise the slot logic.
	legacy := func(exclusive bool) *Config {
		c := &Config{Exclusive: exclusive}
		c.Backups.Daily = BackupSchedule{Time: "01:15", Filesystem: fs}
		c.Backups.Weekly = BackupSchedule{Time: "02:15", DayOfWeek: "Saturday", Filesystem: fs}
		c.Backups.Monthly = BackupSchedule{Time: "03:15", DayOfMonth: 1, Filesystem: fs}
		return c
	}

	tests := []struct {
		name string
		cfg  *Config
		now  time.Time
		want []string
	}{
		{"daysOfWeek hit", scheme(false), at(5, 1, 15), []string{"daily"}},
		{"daysOfWeek miss (Monday)", scheme(false), at(4, 1, 15), nil},
		{"daysOfWeek miss (Sunday)", scheme(false), at(3, 1, 15), nil},
		{"daysOfWeek miss at other time", scheme(false), at(5, 1, 16), nil},
		{"daysOfWeek unset runs every day", legacy(false), at(3, 1, 15), []string{"daily"}},

		{"weekly on Saturday, exclusive", scheme(true), at(9, 2, 15), []string{"weekly"}},
		{"weekly on Saturday, not exclusive", scheme(false), at(9, 2, 15), []string{"weekly"}},
		{"monthly on a Friday, exclusive: daily suppressed", scheme(true), at(1, 1, 15), nil},
		{"monthly on a Friday, exclusive", scheme(true), at(1, 3, 15), []string{"monthly"}},
		{"monthly on a Friday, not exclusive: daily runs", scheme(false), at(1, 1, 15), []string{"daily"}},

		{"daily on weekly day, exclusive: suppressed", legacy(true), at(9, 1, 15), nil},
		{"daily on weekly day, not exclusive", legacy(false), at(9, 1, 15), []string{"daily"}},
		{"daily on normal day, exclusive", legacy(true), at(5, 1, 15), []string{"daily"}},
		{"daily on monthly day, exclusive: suppressed", legacy(true), at(1, 1, 15), nil},
		{"weekly on monthly day, exclusive: suppressed", legacy(true), at(1, 2, 15), nil},

		{"1st is the weekly weekday, exclusive: only monthly", func() *Config {
			c := legacy(true)
			c.Backups.Monthly.DayOfMonth = 2 // Saturday 2024-03-02
			return c
		}(), at(2, 3, 15), []string{"monthly"}},
		{"1st is the weekly weekday, exclusive: weekly suppressed", func() *Config {
			c := legacy(true)
			c.Backups.Monthly.DayOfMonth = 2
			return c
		}(), at(2, 2, 15), nil},
		{"1st is the weekly weekday, not exclusive: both run", func() *Config {
			c := legacy(false)
			c.Backups.Monthly.DayOfMonth = 2
			c.Backups.Monthly.Time = "02:15"
			return c
		}(), at(2, 2, 15), []string{"weekly", "monthly"}},

		{"same slot, exclusive: highest wins", func() *Config {
			c := legacy(true)
			c.Backups.Weekly.Time, c.Backups.Monthly.Time = "01:15", "01:15"
			c.Backups.Monthly.DayOfMonth = 9
			return c
		}(), at(9, 1, 15), []string{"monthly"}},

		{"unconfigured higher tier does not suppress", func() *Config {
			c := legacy(true)
			c.Backups.Weekly = BackupSchedule{Time: "02:15", DayOfWeek: "Saturday"} // no storage target
			return c
		}(), at(9, 1, 15), []string{"daily"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := dueBackups(tt.cfg, tt.now)
			if strings.Join(got, ",") != strings.Join(tt.want, ",") {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDueBackupsOrderIndependent(t *testing.T) {
	// The decision for a tier must not depend on whether higher tiers were already handled.
	c := &Config{Exclusive: true}
	fs := &StorageTarget{RetainCount: 1}
	c.Backups.Daily = BackupSchedule{Time: "01:15", Filesystem: fs}
	c.Backups.Weekly = BackupSchedule{Time: "02:15", DayOfWeek: "Saturday", Filesystem: fs}
	now := time.Date(2024, 3, 9, 1, 15, 0, 0, time.UTC)
	for i := 0; i < 2; i++ {
		if run, reason := decideBackup(c, "daily", now); run || reason != "weekly is due today" {
			t.Errorf("run=%v reason=%q", run, reason)
		}
	}
}

func TestDecideBackupSkipReason(t *testing.T) {
	c := &Config{}
	c.Backups.Daily = BackupSchedule{Time: "01:15", DaysOfWeek: []string{"Tuesday"}, RetainCount: 1}
	// Monday 2024-03-04
	run, reason := decideBackup(c, "daily", time.Date(2024, 3, 4, 1, 15, 0, 0, time.UTC))
	if run || reason != "Monday is not in daysOfWeek" {
		t.Errorf("run=%v reason=%q", run, reason)
	}
	// Not the scheduled minute: silent.
	if run, reason := decideBackup(c, "daily", time.Date(2024, 3, 4, 1, 16, 0, 0, time.UTC)); run || reason != "" {
		t.Errorf("run=%v reason=%q", run, reason)
	}
}

func TestLoadConfigDaysOfWeekAndExclusive(t *testing.T) {
	dir := t.TempDir()
	load := func(content string) (*Config, error) {
		p := filepath.Join(dir, "c.json")
		if err := os.WriteFile(p, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
		return loadConfig(p)
	}

	c, err := load(`{"exclusive": true, "backups": {"daily": {"time": "01:15", "daysOfWeek": ["Tuesday", "Friday"]}}}`)
	if err != nil {
		t.Fatal(err)
	}
	if !c.Exclusive || len(c.Backups.Daily.DaysOfWeek) != 2 {
		t.Errorf("unexpected config: %+v", c)
	}

	// Old configuration without the new fields.
	c, err = load(`{"backups": {"daily": {"time": "01:15", "retainCount": 3}}}`)
	if err != nil {
		t.Fatal(err)
	}
	if c.Exclusive || c.Backups.Daily.DaysOfWeek != nil {
		t.Errorf("new fields must default to unset: %+v", c)
	}

	for name, content := range map[string]string{
		"unknown name": `{"backups": {"daily": {"daysOfWeek": ["Tuesday", "Funday"]}}}`,
		"wrong case":   `{"backups": {"daily": {"daysOfWeek": ["tuesday"]}}}`,
		"on weekly":    `{"backups": {"weekly": {"daysOfWeek": ["Tuesday"]}}}`,
		"on monthly":   `{"backups": {"monthly": {"daysOfWeek": ["Tuesday"]}}}`,
	} {
		if _, err := load(content); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
	if _, err := load(`{"backups": {"daily": {"daysOfWeek": ["Funday"]}}}`); err == nil || !strings.Contains(err.Error(), "Funday") {
		t.Errorf("error should name the bad weekday, got %v", err)
	}
}
