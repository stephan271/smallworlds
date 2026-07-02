#!/usr/bin/env bash
# SmallWorlds External Watchdog Script
# This script is intended to run on an external host (VPS, Raspberry Pi, etc.)
# It monitors the cluster's health endpoint and triggers alerts if the cluster goes offline.

# Load configuration
if [ -f "watchdog-config.env" ]; then
  source "watchdog-config.env"
else
  echo "Error: watchdog-config.env not found!"
  exit 1
fi

FAIL_COUNT_FILE="/tmp/smallworlds_watchdog_fails"
# Initialize if not exists
if [ ! -f "$FAIL_COUNT_FILE" ]; then
  echo "0" > "$FAIL_COUNT_FILE"
fi

FAIL_COUNT=$(cat "$FAIL_COUNT_FILE")

# Check endpoint
HTTP_STATUS=$(curl -s -o /dev/null -w "%{http_code}" "$HEALTH_ENDPOINT")

if [ "$HTTP_STATUS" -ne 200 ]; then
  FAIL_COUNT=$((FAIL_COUNT + 1))
  echo "$FAIL_COUNT" > "$FAIL_COUNT_FILE"
  echo "Health check failed (Status: $HTTP_STATUS). Consecutive failures: $FAIL_COUNT"
  
  if [ "$FAIL_COUNT" -eq "$MAX_FAILURES" ]; then
    echo "Maximum failures reached! Triggering alert..."
    # Send email alert via external SMTP
    if [ -n "$SMTP_HOST" ]; then
      echo "Subject: CRITICAL: SmallWorlds Cluster Offline" | curl --url "smtps://$SMTP_HOST:$SMTP_PORT" --ssl-reqd \
        --mail-from "$SMTP_USER" --mail-rcpt "$ALERT_EMAIL" \
        --user "$SMTP_USER:$SMTP_PASS" \
        -T -
    fi
    
    # Optionally trigger Hetzner API reboot
    if [ "$ENABLE_AUTO_REBOOT" = "true" ] && [ -n "$HCLOUD_TOKEN" ] && [ -n "$HETZNER_SERVER_ID" ]; then
      echo "Triggering Hetzner Server Reboot..."
      curl -X POST -H "Authorization: Bearer $HCLOUD_TOKEN" "https://api.hetzner.cloud/v1/servers/$HETZNER_SERVER_ID/actions/reset"
    fi
  fi
else
  # Reset fail count if it was previously failing
  if [ "$FAIL_COUNT" -gt 0 ]; then
    echo "Cluster recovered. Resetting failure count."
    echo "0" > "$FAIL_COUNT_FILE"
    
    if [ "$FAIL_COUNT" -ge "$MAX_FAILURES" ]; then
      # Send recovery email
      if [ -n "$SMTP_HOST" ]; then
        echo "Subject: RESOLVED: SmallWorlds Cluster Online" | curl --url "smtps://$SMTP_HOST:$SMTP_PORT" --ssl-reqd \
          --mail-from "$SMTP_USER" --mail-rcpt "$ALERT_EMAIL" \
          --user "$SMTP_USER:$SMTP_PASS" \
          -T -
      fi
    fi
  fi
fi
