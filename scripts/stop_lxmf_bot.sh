#!/usr/bin/env bash
echo "Stopping LXMF StiQRcloud Gateway Bot..."
pkill -f "scripts/lxmf_stiqrcloud_bot.py" 2>/dev/null || true
echo "LXMF Bot stopped."
