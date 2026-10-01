package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Conversoes de cada fonte de log para sysLogEntry. Ficam sem build tag para
// serem testadas em qualquer sistema.

func journalPriorityLevel(p int) string {
	switch {
	case p <= 2:
		return "critical"
	case p == 3:
		return "error"
	case p == 4:
		return "warning"
	default:
		return "info"
	}
}

// journalPriorityRange devolve o argumento de "journalctl -p".
func journalPriorityRange(minLevel string) string {
	switch minLevel {
	case "critical":
		return "0..2"
	case "warning":
		return "0..4"
	case "info":
		return "0..6"
	default:
		return "0..3"
	}
}

// windowsEventLevel: 1 critico, 2 erro, 3 aviso, 4 e 0 informacao.
func windowsEventLevel(l int) string {
	switch l {
	case 1:
		return "critical"
	case 2:
		return "error"
	case 3:
		return "warning"
	default:
		return "info"
	}
}

func windowsLevelXPath(minLevel string) string {
	switch minLevel {
	case "critical":
		return "Level=1"
	case "warning":
		return "Level=1 or Level=2 or Level=3"
	case "info":
		return "Level=0 or Level=1 or Level=2 or Level=3 or Level=4"
	default:
		return "Level=1 or Level=2"
	}
}

func macMessageTypeLevel(t string) string {
	switch strings.ToLower(t) {
	case "fault":
		return "critical"
	case "error":
		return "error"
	default:
		return "info"
	}
}

func macLogPredicate(minLevel string) string {
	if minLevel == "info" {
		return "messageType == default OR messageType == error OR messageType == fault"
	}
	if minLevel == "critical" {
		return "messageType == fault"
	}
	return "messageType == error OR messageType == fault"
}

// journalFieldString le um campo do "journalctl -o json": texto, lista de bytes
// (mensagens nao UTF-8) ou lista de valores quando o campo se repete.
func journalFieldString(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	switch raw[0] {
	case '"':
		var s string
		if err := json.Unmarshal(raw, &s); err == nil {
			return s
		}
	case '[':
		var nums []int
		if err := json.Unmarshal(raw, &nums); err == nil {
			b := make([]byte, 0, len(nums))
			for _, n := range nums {
				b = append(b, byte(n))
			}
			return string(b)
		}
		var parts []json.RawMessage
		if err := json.Unmarshal(raw, &parts); err == nil {
			out := make([]string, 0, len(parts))
			for _, p := range parts {
				out = append(out, journalFieldString(p))
			}
			return strings.Join(out, "\n")
		}
	}
	return string(raw)
}

// parseJournalLine converte uma linha do journalctl -o json; devolve tambem o cursor.
func parseJournalLine(line []byte) (sysLogEntry, string, bool) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(line, &m); err != nil {
		return sysLogEntry{}, "", false
	}
	cursor := journalFieldString(m["__CURSOR"])
	if cursor == "" {
		return sysLogEntry{}, "", false
	}
	prio := 6
	if p, err := strconv.Atoi(journalFieldString(m["PRIORITY"])); err == nil {
		prio = p
	}
	source := journalFieldString(m["SYSLOG_IDENTIFIER"])
	if source == "" {
		source = journalFieldString(m["_SYSTEMD_UNIT"])
	}
	if source == "" {
		source = journalFieldString(m["_COMM"])
	}
	ts := time.Now().UTC()
	if us, err := strconv.ParseInt(journalFieldString(m["__REALTIME_TIMESTAMP"]), 10, 64); err == nil {
		ts = time.Unix(us/1e6, (us%1e6)*1e3).UTC()
	}
	e := sysLogEntry{
		Time:    ts.Format(time.RFC3339Nano),
		Level:   journalPriorityLevel(prio),
		Source:  source,
		Log:     "journal",
		Message: journalFieldString(m["MESSAGE"]),
	}
	if h := journalFieldString(m["_HOSTNAME"]); h != "" {
		e.Host = &h
	}
	return e, cursor, true
}

const macLogTimeLayout = "2006-01-02 15:04:05.000000-0700"

// parseMacLogLine converte uma linha do "log show --style ndjson".
func parseMacLogLine(line []byte, host string) (sysLogEntry, time.Time, bool) {
	var m struct {
		Timestamp        string `json:"timestamp"`
		MessageType      string `json:"messageType"`
		EventMessage     string `json:"eventMessage"`
		Subsystem        string `json:"subsystem"`
		ProcessImagePath string `json:"processImagePath"`
		EventType        string `json:"eventType"`
	}
	if err := json.Unmarshal(line, &m); err != nil || m.Timestamp == "" {
		return sysLogEntry{}, time.Time{}, false
	}
	ts, err := time.Parse(macLogTimeLayout, m.Timestamp)
	if err != nil {
		return sysLogEntry{}, time.Time{}, false
	}
	source := m.Subsystem
	if source == "" && m.ProcessImagePath != "" {
		source = filepath.Base(strings.ReplaceAll(m.ProcessImagePath, "\\", "/"))
	}
	e := sysLogEntry{
		Time:    ts.UTC().Format(time.RFC3339Nano),
		Level:   macMessageTypeLevel(m.MessageType),
		Source:  source,
		Log:     "unified",
		Message: m.EventMessage,
	}
	if host != "" {
		h := host
		e.Host = &h
	}
	return e, ts, true
}

// windowsEventScript monta o PowerShell que le, por log, os eventos com RecordId
// maior que o ultimo enviado e ate o ultimo existente no inicio da leitura.
// after < 0 significa primeira leitura: so devolve o ultimo RecordId.
func windowsEventScript(logs []string, after map[string]int64, minLevel string, readCap int) string {
	var sb strings.Builder
	sb.WriteString("$ErrorActionPreference='Stop'\n[Console]::OutputEncoding=[Text.Encoding]::UTF8\n")
	sb.WriteString(`function Read-WcLog($n,$after,$cap,$lv){
  $last=[int64]0
  try { $e=Get-WinEvent -LogName $n -MaxEvents 1 -ErrorAction Stop; if($e){$last=[int64]$e.RecordId} }
  catch { if($_.FullyQualifiedErrorId -notmatch 'NoMatchingEventsFound'){ return @{name=$n;ok=$false;error=$_.Exception.Message;last=0;events=@()} } }
  if($after -lt 0 -or $last -le $after){ return @{name=$n;ok=$true;last=$last;events=@()} }
  $xp="*[System[EventRecordID > $after and EventRecordID <= $last and ($lv)]]"
  $ev=@()
  try { $ev=@(Get-WinEvent -LogName $n -FilterXPath $xp -Oldest -MaxEvents $cap -ErrorAction Stop) }
  catch { if($_.FullyQualifiedErrorId -notmatch 'NoMatchingEventsFound'){ return @{name=$n;ok=$false;error=$_.Exception.Message;last=0;events=@()} } }
  $items=@($ev | ForEach-Object { @{id=[int64]$_.RecordId;t=$_.TimeCreated.ToUniversalTime().ToString('o');lv=[int]$_.Level;src=[string]$_.ProviderName;eid=[int64]$_.Id;msg=[string]$_.Message;host=[string]$_.MachineName} })
  return @{name=$n;ok=$true;last=$last;events=$items}
}
$res=@()
`)
	for _, l := range logs {
		a, ok := after[l]
		if !ok {
			a = -1
		}
		fmt.Fprintf(&sb, "$res+=,(Read-WcLog '%s' %d %d '%s')\n", strings.ReplaceAll(l, "'", "''"), a, readCap, windowsLevelXPath(minLevel))
	}
	sb.WriteString("ConvertTo-Json -InputObject @($res) -Depth 5 -Compress\n")
	return sb.String()
}

type windowsLogResult struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Error  string `json:"error"`
	Last   int64  `json:"last"`
	Events []struct {
		ID      int64  `json:"id"`
		Time    string `json:"t"`
		Level   int    `json:"lv"`
		Source  string `json:"src"`
		EventID int64  `json:"eid"`
		Message string `json:"msg"`
		Host    string `json:"host"`
	} `json:"events"`
}

// parseWindowsEvents interpreta a saida de windowsEventScript e calcula a nova posicao.
func parseWindowsEvents(out []byte, prev map[string]int64, readCap int) ([]sysLogEntry, map[string]int64, error) {
	next := make(map[string]int64, len(prev))
	for k, v := range prev {
		next[k] = v
	}
	out = bytes.TrimSpace(bytes.TrimPrefix(out, []byte("\xef\xbb\xbf")))
	var results []windowsLogResult
	if err := json.Unmarshal(out, &results); err != nil {
		return nil, next, fmt.Errorf("saida do Get-WinEvent invalida: %w", err)
	}
	var entries []sysLogEntry
	var errs []string
	for _, r := range results {
		if !r.OK {
			errs = append(errs, r.Name+": "+r.Error)
			continue
		}
		before, known := prev[r.Name]
		if !known || r.Last < before {
			// primeira leitura ou log limpo: comeca do fim atual
			next[r.Name] = r.Last
			continue
		}
		pos := before
		for _, ev := range r.Events {
			e := sysLogEntry{
				Time:    ev.Time,
				Level:   windowsEventLevel(ev.Level),
				Source:  ev.Source,
				Log:     r.Name,
				Message: strings.TrimSpace(strings.ReplaceAll(ev.Message, "\r", "")),
			}
			if t, err := time.Parse(time.RFC3339Nano, ev.Time); err == nil {
				e.Time = t.UTC().Format(time.RFC3339Nano)
			}
			id := ev.EventID
			e.EventID = &id
			if ev.Host != "" {
				h := ev.Host
				e.Host = &h
			}
			entries = append(entries, e)
			if ev.ID > pos {
				pos = ev.ID
			}
		}
		if len(r.Events) < readCap {
			pos = r.Last
		}
		next[r.Name] = pos
	}
	if len(errs) > 0 {
		return entries, next, fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return entries, next, nil
}
