//go:build windows
// +build windows

package agent

import (
	"encoding/base64"
	"time"
	"unicode/utf16"
)

const sysLogPlatformLog = "Application"

func collectPlatformLogs(cfg sysLogConfig, pos sysLogPosition, host string, readCap int) ([]sysLogEntry, sysLogPosition, error) {
	script := windowsEventScript(cfg.WindowsLogs, pos.Records, cfg.MinLevel, readCap)
	u := utf16.Encode([]rune(script))
	b := make([]byte, 0, len(u)*2)
	for _, c := range u {
		b = append(b, byte(c), byte(c>>8))
	}
	out, err := runHealthCmd(3*time.Minute, "powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-EncodedCommand", base64.StdEncoding.EncodeToString(b))
	if err != nil {
		return nil, pos, err
	}
	entries, next, perr := parseWindowsEvents([]byte(out), pos.Records, readCap)
	return entries, sysLogPosition{Records: next}, perr
}
