#!/bin/bash
# Módulo maintenance para Linux e macOS (versão própria do módulo 01 do WinCare Pro).
# Executado pelo agente como root.

# shellcheck source=_runtime.sh
. "$(dirname "$0")/_runtime.sh"

# Tamanho em KB de um diretório (0 se não existir).
wc_dir_kb() {
	if [ -d "$1" ]; then
		du -sk "$1" 2>/dev/null | awk '{print $1+0}'
	else
		echo 0
	fi
}

task_temp_files() {
	wc_log INFO "Removendo arquivos de /tmp com mais de 7 dias..."
	local root=/tmp/ files dirs
	# -delete trabalha relativo ao diretório já aberto, sem seguir links trocados durante a varredura.
	files=$(find "$root" -xdev -mindepth 1 -type f -mtime +7 -print -delete 2>/dev/null | wc -l | tr -d ' ')
	wc_progress 70
	dirs=$(find "$root" -xdev -mindepth 1 -type d -empty -mtime +7 -print -delete 2>/dev/null | wc -l | tr -d ' ')
	wc_log SUCCESS "Removidos: $files arquivo(s) e $dirs pasta(s) vazia(s) antigas em /tmp"
	wc_result "\"files\":$files,\"dirs\":$dirs"
}

task_package_cache() {
	local done_any=0 before after
	if command -v apt-get >/dev/null 2>&1; then
		before=$(wc_dir_kb /var/cache/apt/archives)
		wc_log INFO "apt-get clean..."
		apt-get clean 2>&1 | wc_log_lines INFO "  "
		after=$(wc_dir_kb /var/cache/apt/archives)
		wc_log SUCCESS "Cache do apt: $((before - after)) KB liberados"
		done_any=1
	fi
	if command -v dnf >/dev/null 2>&1; then
		before=$(wc_dir_kb /var/cache/dnf)
		wc_log INFO "dnf clean all..."
		dnf clean all 2>&1 | wc_log_lines INFO "  "
		after=$(wc_dir_kb /var/cache/dnf)
		wc_log SUCCESS "Cache do dnf: $((before - after)) KB liberados"
		done_any=1
	elif command -v yum >/dev/null 2>&1; then
		before=$(wc_dir_kb /var/cache/yum)
		wc_log INFO "yum clean all..."
		yum clean all 2>&1 | wc_log_lines INFO "  "
		after=$(wc_dir_kb /var/cache/yum)
		wc_log SUCCESS "Cache do yum: $((before - after)) KB liberados"
		done_any=1
	fi
	if command -v zypper >/dev/null 2>&1; then
		before=$(wc_dir_kb /var/cache/zypp)
		wc_log INFO "zypper clean --all..."
		zypper --non-interactive clean --all 2>&1 | wc_log_lines INFO "  "
		after=$(wc_dir_kb /var/cache/zypp)
		wc_log SUCCESS "Cache do zypper: $((before - after)) KB liberados"
		done_any=1
	fi
	local brew="" owner
	for b in /opt/homebrew/bin/brew /usr/local/bin/brew /home/linuxbrew/.linuxbrew/bin/brew; do
		if [ -x "$b" ]; then
			brew=$b
			break
		fi
	done
	if [ -n "$brew" ]; then
		# O Homebrew recusa rodar como root: executa como o dono da instalação.
		if [ "$WC_OS" = "Darwin" ]; then
			owner=$(stat -f %Su "$brew" 2>/dev/null)
		else
			owner=$(stat -c %U "$brew" 2>/dev/null)
		fi
		if [ -n "$owner" ] && [ "$owner" != "root" ]; then
			wc_log INFO "brew cleanup (como $owner)..."
			sudo -u "$owner" -H "$brew" cleanup 2>&1 | wc_log_lines INFO "  "
			wc_log SUCCESS "Cache do Homebrew limpo"
			done_any=1
		else
			wc_log WARN "Homebrew encontrado, mas pertence ao root; limpeza não executada"
		fi
	fi
	if [ "$done_any" -eq 0 ]; then
		wc_skip "Nenhum gerenciador de pacotes suportado encontrado (apt, dnf, yum, zypper, brew)"
	fi
	return 0
}

task_journal_vacuum() {
	if ! command -v journalctl >/dev/null 2>&1; then
		wc_skip "journalctl não encontrado (sistema sem systemd-journald)"
		return 0
	fi
	journalctl --disk-usage 2>&1 | wc_log_lines INFO "Antes: "
	journalctl --vacuum-time=7d 2>&1 | wc_log_lines INFO "  "
	journalctl --disk-usage 2>&1 | wc_log_lines INFO "Depois: "
	wc_log SUCCESS "Logs do journald com mais de 7 dias removidos"
}

task_dns_flush() {
	if [ "$WC_OS" = "Darwin" ]; then
		wc_log INFO "Limpando o cache DNS do macOS..."
		dscacheutil -flushcache 2>&1 | wc_log_lines WARN "  "
		killall -HUP mDNSResponder 2>&1 | wc_log_lines WARN "  "
		wc_log SUCCESS "Cache DNS limpo"
		return 0
	fi
	if command -v resolvectl >/dev/null 2>&1 && resolvectl flush-caches >/dev/null 2>&1; then
		wc_log SUCCESS "Cache DNS do systemd-resolved limpo (resolvectl flush-caches)"
		return 0
	fi
	if command -v systemd-resolve >/dev/null 2>&1 && systemd-resolve --flush-caches >/dev/null 2>&1; then
		wc_log SUCCESS "Cache DNS do systemd-resolved limpo (systemd-resolve --flush-caches)"
		return 0
	fi
	if command -v nscd >/dev/null 2>&1 && nscd -i hosts >/dev/null 2>&1; then
		wc_log SUCCESS "Cache de hosts do nscd invalidado"
		return 0
	fi
	wc_skip "Nenhum cache DNS local ativo encontrado (systemd-resolved ou nscd)"
}

task_disk_space() {
	local warn=0 rows="" fs size used avail pct mnt ipct
	wc_log INFO "--- ESPAÇO EM DISCO ---"
	local dfout
	if [ "$WC_OS" = "Darwin" ]; then
		dfout=$(df -P -k 2>/dev/null | awk 'NR>1 && $1 !~ /^(devfs|map)/')
	else
		dfout=$(df -P -k -x tmpfs -x devtmpfs -x squashfs -x efivarfs -x overlay 2>/dev/null | awk 'NR>1')
		[ -z "$dfout" ] && dfout=$(df -P -k 2>/dev/null | awk 'NR>1 && $1 !~ /^(tmpfs|devtmpfs)$/')
	fi
	while read -r fs size used avail pct mnt; do
		[ -z "$fs" ] && continue
		pct=${pct%\%}
		local level=INFO
		if [ "$pct" -ge 90 ] 2>/dev/null; then
			level=WARN
			warn=1
		fi
		wc_log "$level" "  $mnt ($fs): $((avail / 1024)) MB livres de $((size / 1024)) MB ($pct% usado)"
		ipct=$(wc_inode_pct "$mnt")
		if [ -n "$ipct" ]; then
			if [ "$ipct" -ge 90 ] 2>/dev/null; then
				wc_log WARN "  $mnt: inodes $ipct% usados"
				warn=1
			else
				wc_log INFO "  $mnt: inodes $ipct% usados"
			fi
		fi
		[ -n "$rows" ] && rows="$rows,"
		rows="$rows{\"mount\":$(wc_jstr "$mnt"),\"filesystem\":$(wc_jstr "$fs"),\"sizeKB\":$size,\"freeKB\":$avail,\"usedPercent\":$pct,\"inodesUsedPercent\":${ipct:-null}}"
	done <<EOF
$dfout
EOF
	wc_result "\"volumes\":[$rows]"
	if [ "$warn" -eq 0 ]; then
		wc_log SUCCESS "Espaço e inodes dentro do limite (abaixo de 90%)"
	fi
}

# Percentual de inodes usados de um ponto de montagem (vazio se o sistema de arquivos não informa).
wc_inode_pct() {
	local p
	if [ "$WC_OS" = "Darwin" ]; then
		p=$(df -i "$1" 2>/dev/null | awk 'NR==2 {print $8}')
	else
		p=$(df -P -i "$1" 2>/dev/null | awk 'NR==2 {print $5}')
	fi
	p=${p%\%}
	case $p in
	'' | *[!0-9]*) return 0 ;;
	esac
	printf '%s' "$p"
}

wc_main
