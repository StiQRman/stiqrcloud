#!/usr/bin/env bash
echo "Stopping LXMF TestCloud Gateway Bot..."
pkill -f "scripts/lxmf_testcloud_bot.py" 2>/dev/null || true
echo "LXMF Bot stopped."
