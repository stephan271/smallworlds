# SmallWorlds External Watchdog

The watchdog is a lightweight script that runs on an **external host** (like a cheap VPS, a Raspberry Pi at home, or a free-tier cloud function). It monitors the cluster from the outside.

## Why is this needed?
If your single-node K3s cluster crashes entirely (e.g., VM kernel panic, OOM, Hetzner network issue), the internal Hermes AI agent cannot notify you because it is dead. The watchdog provides a simple external safety net.

## Installation

1. Copy the `watchdog.sh` and `watchdog-config.env` files to your external host.
2. Edit `watchdog-config.env` and fill in your external SMTP details and Hetzner API token.
3. Make the script executable:
   ```bash
   chmod +x watchdog.sh
   ```
4. Add it to crontab to run every minute:
   ```bash
   crontab -e
   ```
   Add the following line:
   ```text
   * * * * * cd /path/to/watchdog && ./watchdog.sh >> watchdog.log 2>&1
   ```

## Testing

You can test the alert by temporarily changing the `HEALTH_ENDPOINT` in `watchdog-config.env` to a non-existent URL (e.g., `https://status.smallworlds.network/does-not-exist`). After 3 minutes, you should receive an email alert.
