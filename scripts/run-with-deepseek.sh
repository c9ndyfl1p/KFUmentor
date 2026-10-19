#!/bin/sh
set -eu

app_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
path_file="$app_dir/scripts/.deepseek-bridge-path.local"
bridge_dir=${DEEPSEEK_BRIDGE_DIR:-}
if [ -z "$bridge_dir" ] && [ -f "$path_file" ]; then
	IFS= read -r bridge_dir < "$path_file"
fi
if [ -z "$bridge_dir" ]; then
	bridge_dir="$app_dir/../DeepSeek-Api-Free"
fi
if [ ! -f "$bridge_dir/.env" ] || [ ! -x "$bridge_dir/.venv/bin/python" ]; then
	printf '%s\n' "Не найден настроенный DeepSeek bridge: $bridge_dir" >&2
	exit 1
fi

api_key=$("$bridge_dir/.venv/bin/python" -c 'from dotenv import dotenv_values; import sys; print(dotenv_values(sys.argv[1]).get("API_KEY") or "")' "$bridge_dir/.env")
if [ -z "$api_key" ]; then
	printf '%s\n' 'Задайте API_KEY в .env моста DeepSeek.' >&2
	exit 1
fi
export AI_JOB_AGENT_MODEL_API_KEY="$api_key"
AI_JOB_AGENT_MODEL_TIMEOUT_SECONDS=${AI_JOB_AGENT_MODEL_TIMEOUT_SECONDS:-900}
export AI_JOB_AGENT_MODEL_TIMEOUT_SECONDS
AI_JOB_AGENT_MODEL_REQUEST_INTERVAL_SECONDS=${AI_JOB_AGENT_MODEL_REQUEST_INTERVAL_SECONDS:-12}
AI_JOB_AGENT_MODEL_RATE_FILE="$app_dir/data/model-rate.local.json"
export AI_JOB_AGENT_MODEL_REQUEST_INTERVAL_SECONDS AI_JOB_AGENT_MODEL_RATE_FILE

bridge_pid=""
cleanup() {
	if [ -n "$bridge_pid" ]; then
		kill "$bridge_pid" 2>/dev/null || true
		wait "$bridge_pid" 2>/dev/null || true
	fi
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

if ! curl -fsS --max-time 2 http://127.0.0.1:8000/healthz >/dev/null 2>&1; then
	if [ ! -x "$bridge_dir/.venv/bin/uvicorn" ]; then
		printf '%s\n' "Не найден uvicorn моста: $bridge_dir/.venv/bin/uvicorn" >&2
		exit 1
	fi
	bridge_log="${TMPDIR:-/tmp}/ai-job-agent-deepseek-bridge.log"
	(cd "$bridge_dir" && exec .venv/bin/uvicorn app.main:app --host 127.0.0.1 --port 8000) >"$bridge_log" 2>&1 &
	bridge_pid=$!
	ready=0
	for _ in $(seq 1 60); do
		if curl -fsS --max-time 2 http://127.0.0.1:8000/healthz >/dev/null 2>&1; then
			ready=1
			break
		fi
		if ! kill -0 "$bridge_pid" 2>/dev/null; then
			break
		fi
		sleep 1
	done
	if [ "$ready" -ne 1 ]; then
		printf '%s\n' "Не удалось запустить мост DeepSeek. Проверьте его настройки и журнал: $bridge_log" >&2
		exit 1
	fi
fi

cd "$app_dir"
go run . --web --model-url=http://127.0.0.1:8000/v1 --model=deepseek-chat "$@"
