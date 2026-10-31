// Package backup writes compressed pg_dump files and keeps the newest N.
package backup

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"time"

	"github.com/shyam/dashboard/internal/config"
)

const keep = 30

// Run dumps the database to <BackupDir>/dashboard-YYYYMMDD-HHMMSS.sql.gz and prunes old files.
func Run(ctx context.Context, cfg *config.Config) (string, error) {
	if err := os.MkdirAll(cfg.BackupDir, 0o700); err != nil {
		return "", err
	}
	pgdump, err := exec.LookPath("pg_dump")
	if err != nil {
		return "", fmt.Errorf("pg_dump not found on PATH (install Postgres client tools)")
	}
	name := filepath.Join(cfg.BackupDir, "dashboard-"+time.Now().In(cfg.Timezone).Format("20060102-150405")+".sql.gz")
	cmd := exec.CommandContext(ctx, pgdump, "--no-owner", "--no-privileges", "--dbname="+cfg.DatabaseURL)
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("pg_dump: %v: %s", err, stderr.String())
	}
	f, err := os.OpenFile(name, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return "", err
	}
	gz := gzip.NewWriter(f)
	if _, err := gz.Write(out.Bytes()); err != nil {
		f.Close()
		return "", err
	}
	if err := gz.Close(); err != nil {
		f.Close()
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	prune(cfg.BackupDir)
	return name, nil
}

// List returns backup file names, newest first.
func List(cfg *config.Config) ([]string, error) {
	matches, err := filepath.Glob(filepath.Join(cfg.BackupDir, "dashboard-*.sql.gz"))
	if err != nil {
		return nil, err
	}
	sort.Sort(sort.Reverse(sort.StringSlice(matches)))
	for i := range matches {
		matches[i] = filepath.Base(matches[i])
	}
	return matches, nil
}

func prune(dir string) {
	matches, _ := filepath.Glob(filepath.Join(dir, "dashboard-*.sql.gz"))
	sort.Strings(matches)
	for len(matches) > keep {
		_ = os.Remove(matches[0])
		matches = matches[1:]
	}
}

// Daily runs a backup now and then every 24 hours until ctx ends.
func Daily(ctx context.Context, cfg *config.Config, logf func(string, ...any)) {
	run := func() {
		c, cancel := context.WithTimeout(ctx, 5*time.Minute)
		defer cancel()
		if path, err := Run(c, cfg); err != nil {
			logf("backup: %v", err)
		} else {
			logf("backup: wrote %s", path)
		}
	}
	run()
	t := time.NewTicker(24 * time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			run()
		}
	}
}
