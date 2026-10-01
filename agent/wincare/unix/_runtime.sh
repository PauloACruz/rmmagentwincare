# shellcheck shell=bash
# Harness comum dos módulos WinCare para Linux e macOS, executados pelo agente como root.
#
# Protocolo: cada evento é uma linha "##WC {json}" no stdout com os tipos log, progress,
# task e result. O agente acrescenta seq e time e publica.
#
# Entrada (variáveis de ambiente definidas pelo agente):
#   WINCARE_TASKS  chaves das tarefas separadas por vírgula, na ordem de execução
#   WINCARE_DIR    diretório temporário privado desta execução
#
# Cada módulo faz source deste arquivo, define funções task_<chave> e chama wc_main.
# Compatível com o bash 3.2 do macOS (sem arrays associativos nem ${var,,}).

export LC_ALL=C
PATH="$PATH:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
export PATH

WC_OS=$(uname -s 2>/dev/null)
WC_TASK_COUNT=0
WC_TASK_INDEX=0
WC_CURRENT_TASK=""
WC_TASK_WARN=0
WC_TASK_ERR=0
WC_TASK_SKIP=""

# Escapa uma string para uso dentro de aspas em JSON.
wc_json_escape() {
	local s=$1
	s=${s//\\/\\\\}
	s=${s//\"/\\\"}
	s=${s//$'\n'/\\n}
	s=${s//$'\r'/\\r}
	s=${s//$'\t'/\\t}
	case $s in
	*[$'\001'-$'\037']*) s=$(printf '%s' "$s" | tr -d '\001-\037') ;;
	esac
	printf '%s' "$s"
}

wc_emit() {
	printf '##WC %s\n' "$1"
}

# wc_log NIVEL mensagem   (NIVEL: INFO, WARN, ERROR, SUCCESS)
wc_log() {
	local level=$1
	shift
	local msg="$*"
	[ -z "$msg" ] && return 0
	if [ -n "$WC_CURRENT_TASK" ]; then
		case $level in
		ERROR) WC_TASK_ERR=1 ;;
		WARN) WC_TASK_WARN=1 ;;
		esac
	fi
	wc_emit "$(printf '{"type":"log","level":"%s","message":"%s"}' "$level" "$(wc_json_escape "$msg")")"
}

# Envia cada linha da entrada padrão como log no nível informado.
wc_log_lines() {
	local level=${1:-INFO} prefix=${2:-} line
	while IFS= read -r line || [ -n "$line" ]; do
		[ -n "$line" ] && wc_log "$level" "$prefix$line"
	done
}

# wc_progress PERCENTUAL_DA_TAREFA [mensagem]
wc_progress() {
	local v=${1:-0} msg=${2:-}
	[ "$v" -lt 0 ] 2>/dev/null && v=0
	[ "$v" -gt 100 ] 2>/dev/null && v=100
	if [ "$WC_TASK_COUNT" -gt 0 ] && [ -n "$WC_CURRENT_TASK" ]; then
		v=$(( ((WC_TASK_INDEX - 1) * 100 + v) / WC_TASK_COUNT ))
	fi
	if [ -n "$msg" ]; then
		wc_emit "$(printf '{"type":"progress","value":%d,"message":"%s"}' "$v" "$(wc_json_escape "$msg")")"
	else
		wc_emit "$(printf '{"type":"progress","value":%d}' "$v")"
	fi
}

# wc_result 'campos JSON já montados', ex.: wc_result '"seconds":1.2,"rating":"Bom"'
wc_result() {
	local fields=$1
	if [ -n "$fields" ]; then
		wc_emit "$(printf '{"type":"result","data":{"task":"%s",%s}}' "$WC_CURRENT_TASK" "$fields")"
	else
		wc_emit "$(printf '{"type":"result","data":{"task":"%s"}}' "$WC_CURRENT_TASK")"
	fi
}

# String JSON entre aspas, pronta para wc_result.
wc_jstr() {
	printf '"%s"' "$(wc_json_escape "$1")"
}

# Marca a tarefa como ignorada (não se aplica). Use: wc_skip "motivo"; return 0
wc_skip() {
	WC_TASK_SKIP=$1
}

wc_task_event() {
	local key=$1 status=$2 msg=${3:-}
	if [ -n "$msg" ]; then
		wc_emit "$(printf '{"type":"task","key":"%s","status":"%s","message":"%s"}' "$key" "$status" "$(wc_json_escape "$msg")")"
	else
		wc_emit "$(printf '{"type":"task","key":"%s","status":"%s"}' "$key" "$status")"
	fi
}

# Tempo de execução de um comando em segundos com milissegundos (palavra-chave time do bash).
# Uso: wc_time_cmd ARQUIVO_SAIDA comando args...   imprime os segundos, ou "fail" se o comando falhar
wc_time_cmd() {
	local out=$1
	shift
	local TIMEFORMAT=%3R t
	t=$( { time { "$@" >"$out" 2>&1 || printf 'WC_FAIL\n'; }; } 2>&1)
	case $t in
	*WC_FAIL*)
		printf 'fail'
		return 1
		;;
	esac
	printf '%s' "$t" | tail -n 1
}

wc_run_task() {
	local key=$1 rc
	(
		WC_CURRENT_TASK=$key
		WC_TASK_WARN=0
		WC_TASK_ERR=0
		WC_TASK_SKIP=""
		"task_$key"
		rc=$?
		if [ -n "$WC_TASK_SKIP" ]; then
			wc_log INFO "Tarefa ignorada: $WC_TASK_SKIP"
			wc_task_event "$key" skipped "$WC_TASK_SKIP"
		elif [ "$rc" -ne 0 ] || [ "$WC_TASK_ERR" -ne 0 ]; then
			wc_task_event "$key" error
		elif [ "$WC_TASK_WARN" -ne 0 ]; then
			wc_task_event "$key" warning
		else
			wc_task_event "$key" ok
		fi
		exit 77
	)
	rc=$?
	if [ "$rc" -ne 77 ]; then
		wc_log ERROR "A tarefa $key terminou de forma inesperada (código $rc)"
		wc_task_event "$key" error "Código de saída $rc"
	fi
}

wc_main() {
	local tasks=${WINCARE_TASKS:-} key list=""
	local IFS_OLD=$IFS
	IFS=','
	for key in $tasks; do
		list="$list $key"
	done
	IFS=$IFS_OLD
	for key in $list; do
		WC_TASK_COUNT=$((WC_TASK_COUNT + 1))
	done
	if [ "$WC_TASK_COUNT" -eq 0 ]; then
		wc_log ERROR "Nenhuma tarefa informada"
		exit 2
	fi
	wc_progress 0
	for key in $list; do
		WC_TASK_INDEX=$((WC_TASK_INDEX + 1))
		case $key in
		*[!a-z0-9_]*)
			wc_task_event "$key" error "Chave de tarefa inválida"
			continue
			;;
		esac
		if ! command -v "task_$key" >/dev/null 2>&1; then
			wc_task_event "$key" error "Tarefa não implementada neste sistema"
			continue
		fi
		wc_task_event "$key" running
		wc_run_task "$key"
		wc_progress $((WC_TASK_INDEX * 100 / WC_TASK_COUNT))
	done
}
