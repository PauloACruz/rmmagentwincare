//go:build darwin
// +build darwin

package agent

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"os/exec"
	"time"
)

const sysLogPlatformLog = "unified"

func collectPlatformLogs(cfg sysLogConfig, pos sysLogPosition, host string, readCap int) ([]sysLogEntry, sysLogPosition, error) {
	since, err := time.Parse(time.RFC3339Nano, pos.Since)
	if pos.Since == "" || err != nil {
		return nil, sysLogPosition{Since: time.Now().UTC().Format(time.RFC3339Nano)}, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
	defer cancel()
	start := since.Local().Format("2006-01-02 15:04:05-0700")
	cmd := exec.CommandContext(ctx, "/usr/bin/log", "show", "--style", "ndjson", "--start", start, "--predicate", macLogPredicate(cfg.MinLevel))
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, pos, err
	}
	if err := cmd.Start(); err != nil {
		return nil, pos, err
	}

	last := since
	var entries []sysLogEntry
	r := bufio.NewReaderSize(stdout, 64*1024)
	for len(entries) < readCap {
		line, rerr := r.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			if e, ts, ok := parseMacLogLine(line, host); ok && ts.After(since) {
				entries = append(entries, e)
				if ts.After(last) {
					last = ts
				}
			}
		}
		if rerr != nil {
			if !errors.Is(rerr, io.EOF) {
				err = rerr
			}
			break
		}
	}
	if len(entries) >= readCap && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	_ = cmd.Wait()
	if ctx.Err() != nil && err == nil {
		err = ctx.Err()
	}
	return entries, sysLogPosition{Since: last.UTC().Format(time.RFC3339Nano)}, err
}
