//go:build !linux && !windows && !darwin
// +build !linux,!windows,!darwin

package agent

const sysLogPlatformLog = "agent"

func collectPlatformLogs(cfg sysLogConfig, pos sysLogPosition, host string, readCap int) ([]sysLogEntry, sysLogPosition, error) {
	return nil, pos, errSysLogUnsupported
}
