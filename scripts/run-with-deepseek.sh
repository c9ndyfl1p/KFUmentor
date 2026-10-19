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

cd "$app_dir"
exec go run . --web --model-url=http://127.0.0.1:8000/v1 --model=deepseek-chat "$@"
