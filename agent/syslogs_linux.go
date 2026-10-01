//go:build linux
// +build linux

package agent

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"
)

const sysLogPlatformLog = "journal"

func collectPlatformLogs(cfg sysLogConfig, pos sysLogPosition, host string, readCap int) ([]sysLogEntry, sysLogPosition, error) {
	if pos.Cursor == "" && pos.Since == "" {
		next := sysLogPosition{Since: time.Now().UTC().Format(time.RFC3339Nano)}
		cur, err := journalLastCursor()
		if cur != "" {
			next = sysLogPosition{Cursor: cur}
		}
		return nil, next, err
	}

	args := []string{"-o", "json", "--no-pager", "-q", "-p", journalPriorityRange(cfg.MinLevel)}
	if pos.Cursor != "" {
		args = append(args, "--after-cursor", pos.Cursor)
	} else {
		since, err := time.Parse(time.RFC3339Nano, pos.Since)
		if err != nil {
			return nil, sysLogPosition{}, err
		}
		args = append(args, "--since", fmt.Sprintf("@%d", since.Unix()))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "journalctl", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, pos, err
	}
	if err := cmd.Start(); err != nil {
		return nil, pos, err
	}

	next := pos
	var entries []sysLogEntry
	r := bufio.NewReaderSize(stdout, 64*1024)
	for len(entries) < readCap {
		line, rerr := r.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			if e, cursor, ok := parseJournalLine(line); ok {
				entries = append(entries, e)
				next = sysLogPosition{Cursor: cursor}
			}
		}
		if rerr != nil {
			if !errors.Is(rerr, io.EOF) {
				err = rerr
			}
			break
		}
	}
	stopped := len(entries) >= readCap
	if stopped && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	werr := cmd.Wait()
	if err == nil && werr != nil && !stopped && len(entries) == 0 {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = werr.Error()
		}
		err = fmt.Errorf("journalctl: %s", msg)
	}
	return entries, next, err
}

func journalLastCursor() (string, error) {
	out, err := runHealthCmd(30*time.Second, "journalctl", "-n", "1", "-o", "json", "--no-pager", "-q")
	if err != nil {
		return "", fmt.Errorf("journalctl: %v", err)
	}
	for _, line := range strings.Split(out, "\n") {
		if _, cursor, ok := parseJournalLine([]byte(line)); ok {
			return cursor, nil
		}
	}
	return "", nil
}
