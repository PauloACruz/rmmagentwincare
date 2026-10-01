#!/bin/bash
# Módulo component_test para Linux e macOS: testes básicos de CPU, memória, disco e rede
# (versão própria do módulo 08 do WinCare Pro). Executado pelo agente como root.

# shellcheck source=_runtime.sh
. "$(dirname "$0")/_runtime.sh"

WC_WORK=${WINCARE_DIR:-/tmp}

task_cpu_bench() {
	wc_log INFO "--- BENCHMARK DE CPU ---"
	wc_log INFO "  Contando números primos até 200.000..."
	local out="$WC_WORK/cpu_bench.out" secs primes rating
	secs=$(wc_time_cmd "$out" awk 'BEGIN { n = 0; for (i = 2; i <= 200000; i++) { p = 1; for (d = 2; d * d <= i; d++) if (i % d == 0) { p = 0; break }; n += p }; print n }')
	primes=$(tr -dc '0-9' <"$out")
	rm -f "$out"
	case $secs in
	'' | *[!0-9.]*)
		wc_log ERROR "  Não foi possível medir o tempo do benchmark"
		return 1
		;;
	esac
	rating=$(awk -v s="$secs" 'BEGIN { if (s < 1) print "Excelente"; else if (s < 3) print "Bom"; else print "Regular" }')
	wc_log SUCCESS "  $primes primos em ${secs}s: $rating"
	wc_result "\"test\":\"primes\",\"limit\":200000,\"primes\":${primes:-0},\"seconds\":$secs,\"rating\":$(wc_jstr "$rating")"
}

task_ram_info() {
	wc_log INFO "--- MEMÓRIA RAM ---"
	local total_kb avail_kb swap_total swap_free pct
	if [ "$WC_OS" = "Darwin" ]; then
		local bytes pagesize free inactive spec
		bytes=$(sysctl -n hw.memsize 2>/dev/null)
		total_kb=$((bytes / 1024))
		pagesize=$(sysctl -n hw.pagesize 2>/dev/null)
		[ -z "$pagesize" ] && pagesize=4096
		free=$(vm_stat | awk '/Pages free/ {gsub(/\./, "", $3); print $3}')
		inactive=$(vm_stat | awk '/Pages inactive/ {gsub(/\./, "", $3); print $3}')
		spec=$(vm_stat | awk '/Pages speculative/ {gsub(/\./, "", $3); print $3}')
		avail_kb=$(( (${free:-0} + ${inactive:-0} + ${spec:-0}) * pagesize / 1024 ))
		swap_total=0
		swap_free=0
	else
		total_kb=$(awk '/^MemTotal:/ {print $2}' /proc/meminfo)
		avail_kb=$(awk '/^MemAvailable:/ {print $2}' /proc/meminfo)
		[ -z "$avail_kb" ] && avail_kb=$(awk '/^(MemFree|Buffers|Cached):/ {s += $2} END {print s}' /proc/meminfo)
		swap_total=$(awk '/^SwapTotal:/ {print $2}' /proc/meminfo)
		swap_free=$(awk '/^SwapFree:/ {print $2}' /proc/meminfo)
	fi
	if [ -z "$total_kb" ] || [ "$total_kb" -eq 0 ]; then
		wc_log ERROR "  Não foi possível ler a memória total"
		return 1
	fi
	pct=$((avail_kb * 100 / total_kb))
	wc_log INFO "  Total: $((total_kb / 1024)) MB"
	if [ "$pct" -lt 10 ]; then
		wc_log WARN "  Disponível agora: $((avail_kb / 1024)) MB ($pct%)"
	else
		wc_log SUCCESS "  Disponível agora: $((avail_kb / 1024)) MB ($pct%)"
	fi
	if [ "${swap_total:-0}" -gt 0 ]; then
		wc_log INFO "  Swap: $(((swap_total - swap_free) / 1024)) MB usados de $((swap_total / 1024)) MB"
	fi
	wc_result "\"totalMB\":$((total_kb / 1024)),\"availableMB\":$((avail_kb / 1024)),\"availablePercent\":$pct,\"swapTotalMB\":$((${swap_total:-0} / 1024)),\"swapUsedMB\":$(((${swap_total:-0} - ${swap_free:-0}) / 1024))"
}

task_disk_speed() {
	wc_log INFO "--- VELOCIDADE DO DISCO ---"
	local dir=/var/tmp size_mb=256 f wsecs rsecs wmbs rmbs free_kb kind
	[ -d "$dir" ] || dir=$WC_WORK
	free_kb=$(df -P -k "$dir" 2>/dev/null | awk 'NR==2 {print $4}')
	if [ -n "$free_kb" ] && [ "$free_kb" -lt $(( (size_mb + 256) * 1024 )) ]; then
		wc_skip "Espaço livre insuficiente em $dir para o arquivo de teste de ${size_mb} MB"
		return 0
	fi
	f=$(mktemp "$dir/wincare_disk.XXXXXX") || {
		wc_log ERROR "  Não foi possível criar o arquivo temporário"
		return 1
	}
	trap 'rm -f "$f"' EXIT
	if [ "$WC_OS" = "Darwin" ]; then
		wsecs=$(wc_time_cmd /dev/null sh -c 'dd if=/dev/zero of="$1" bs=1m count='"$size_mb"' && sync' sh "$f")
		purge >/dev/null 2>&1
		rsecs=$(wc_time_cmd /dev/null dd if="$f" of=/dev/null bs=1m)
	else
		kind="direto (O_DIRECT)"
		wsecs=$(wc_time_cmd /dev/null dd if=/dev/zero of="$f" bs=1M count="$size_mb" oflag=direct conv=fsync)
		if [ "$(wc -c <"$f" | tr -d ' ')" -ne $((size_mb * 1024 * 1024)) ]; then
			kind="com cache e fsync"
			wsecs=$(wc_time_cmd /dev/null dd if=/dev/zero of="$f" bs=1M count="$size_mb" conv=fsync)
		fi
		rsecs=$(wc_time_cmd /dev/null dd if="$f" of=/dev/null bs=1M iflag=direct)
		case $rsecs in
		'' | *[!0-9.]*) rsecs=$(wc_time_cmd /dev/null dd if="$f" of=/dev/null bs=1M) ;;
		esac
		wc_log INFO "  Modo de E/S: $kind"
	fi
	rm -f "$f"
	trap - EXIT
	case "$wsecs$rsecs" in
	*[!0-9.]* | '')
		wc_log ERROR "  Falha ao medir o disco (escrita: $wsecs, leitura: $rsecs)"
		return 1
		;;
	esac
	wmbs=$(awk -v s="$wsecs" -v m="$size_mb" 'BEGIN { if (s <= 0) s = 0.001; printf "%.1f", m / s }')
	rmbs=$(awk -v s="$rsecs" -v m="$size_mb" 'BEGIN { if (s <= 0) s = 0.001; printf "%.1f", m / s }')
	wc_log SUCCESS "  Escrita sequencial : $wmbs MB/s (${size_mb} MB em ${wsecs}s)"
	wc_log SUCCESS "  Leitura sequencial : $rmbs MB/s (${size_mb} MB em ${rsecs}s)"
	wc_log INFO "  Arquivo de teste removido"
	wc_result "\"sizeMB\":$size_mb,\"path\":$(wc_jstr "$dir"),\"writeMBps\":$wmbs,\"readMBps\":$rmbs,\"writeSeconds\":$wsecs,\"readSeconds\":$rsecs"
}

task_net_ping() {
	wc_log INFO "--- CONECTIVIDADE ---"
	local rows="" target out avg ok_count=0 total=0 line
	if ! command -v ping >/dev/null 2>&1; then
		wc_log WARN "  Comando ping não encontrado"
	else
		for target in 8.8.8.8 1.1.1.1 google.com cloudflare.com; do
			total=$((total + 1))
			if [ "$WC_OS" = "Darwin" ]; then
				out=$(ping -c 4 -t 10 "$target" 2>&1)
			else
				out=$(ping -c 4 -W 2 "$target" 2>&1)
			fi
			line=$(printf '%s\n' "$out" | grep 'min/avg/max')
			[ -n "$rows" ] && rows="$rows,"
			if [ -n "$line" ]; then
				avg=$(printf '%s' "$line" | awk -F'=' '{print $2}' | awk -F'/' '{gsub(/ /, "", $2); print $2}')
				wc_log SUCCESS "  $target: média ${avg} ms"
				rows="$rows{\"target\":\"$target\",\"ok\":true,\"avgMs\":${avg:-null}}"
				ok_count=$((ok_count + 1))
			else
				wc_log WARN "  $target: SEM RESPOSTA ($(printf '%s' "$out" | tail -n 1))"
				rows="$rows{\"target\":\"$target\",\"ok\":false}"
			fi
		done
	fi
	local dns="" name addr
	for name in google.com cloudflare.com; do
		addr=""
		if command -v getent >/dev/null 2>&1; then
			addr=$(getent hosts "$name" 2>/dev/null | awk '{print $1}' | head -n 3 | tr '\n' ' ')
		elif [ "$WC_OS" = "Darwin" ]; then
			addr=$(dscacheutil -q host -a name "$name" 2>/dev/null | awk '/_address:/ {print $2}' | head -n 3 | tr '\n' ' ')
		elif command -v host >/dev/null 2>&1; then
			addr=$(host "$name" 2>/dev/null | awk '/has address/ {print $4}' | head -n 3 | tr '\n' ' ')
		fi
		addr=${addr% }
		[ -n "$dns" ] && dns="$dns,"
		if [ -n "$addr" ]; then
			wc_log SUCCESS "  DNS $name: $addr"
			dns="$dns{\"name\":\"$name\",\"ok\":true,\"addresses\":$(wc_jstr "$addr")}"
		else
			wc_log WARN "  DNS $name: falhou"
			dns="$dns{\"name\":\"$name\",\"ok\":false}"
		fi
	done
	wc_result "\"ping\":[$rows],\"dns\":[$dns]"
}

wc_main
