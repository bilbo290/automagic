#!/bin/bash

# Test script to verify daemon responds to Ctrl+C
echo "Testing daemon signal handling..."
echo "This script will:"
echo "1. Start the daemon"
echo "2. Wait 15 seconds"
echo "3. Send SIGINT (Ctrl+C) signal"
echo "4. Verify the daemon stops gracefully"
echo ""

# Build first
echo "Building..."
go build

# Start daemon in background
echo "Starting daemon in dry-run mode..."
timeout 20s ./peter -daemon -dry-run &
DAEMON_PID=$!

# Wait a bit for daemon to start
sleep 5

echo "Daemon started with PID: $DAEMON_PID"
echo "Waiting 10 seconds before sending signal..."
sleep 10

echo "Sending SIGINT to daemon..."
kill -INT $DAEMON_PID

# Wait for graceful shutdown
echo "Waiting for daemon to stop..."
wait $DAEMON_PID
EXIT_CODE=$?

if [ $EXIT_CODE -eq 0 ] || [ $EXIT_CODE -eq 130 ]; then
    echo "✅ Daemon stopped gracefully!"
else
    echo "❌ Daemon did not stop gracefully (exit code: $EXIT_CODE)"
fi

echo "Test completed."