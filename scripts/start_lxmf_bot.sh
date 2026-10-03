#!/usr/bin/env bash
set -e

DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
LOG_FILE="$DIR/data/logs/lxmf_bot.log"
mkdir -p "$DIR/data/logs"

# Check if already running
if pgrep -f "scripts/lxmf_testcloud_bot.py" > /dev/null; then
    echo "LXMF Bot is already running."
    exit 0
fi

echo "Starting LXMF TestCloud Gateway Bot..."
nohup python3 "$DIR/scripts/lxmf_testcloud_bot.py" >> "$LOG_FILE" 2>&1 &
BOT_PID=$!
echo "LXMF Bot started with PID $BOT_PID. Logs: $LOG_FILE"
