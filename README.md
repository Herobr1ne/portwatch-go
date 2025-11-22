# PortWatch – TCP Monitor with MTR and Discord Alerts

PortWatch is a lightweight Go-based monitoring tool for IPv4 and IPv6 TCP ports.  
If a port becomes unreachable, PortWatch sends a Discord alert and generates an MTR trace, saved locally and included in the alert.  
When the port recovers, PortWatch sends an UP notification with full downtime duration.  
All targets run in parallel goroutines and support custom timezones, log rotation, and multiple IP versions.

---

## Features

- Monitor multiple IPv4 and IPv6 targets
- Parallel monitoring using goroutines (non-blocking)
- DOWN detection with automatic MTR
- UP recovery alerts with downtime duration
- Discord webhook notifications (embeds)
- Saves MTR traces into `./mtr/`
- Log rotation at 50 MB (`portwatch.log`, `portwatch.log.1`)
- Custom timezone support
- Supports both array-based and legacy single-target configs

---

## Dependencies

### Go (1.20 or newer)
```bash
sudo apt update
sudo apt install -y golang-go
sudo apt install -y mtr
```
### Build and Run
```bash
git clone https://github.com/Herobr1ne/portwatch-go.git
cd portwatch-go
go build -o portwatch
./portwatch
```
### Config File Example (Extended)
```bash
{
  "webhook": "https://discord.com/api/webhooks/YOUR_WEBHOOK",
  "hostname": "",
  "delay": 1,
  "timeout": 5,
  "timezone": "Europe/Berlin",
  "targets": [
    {
      "name": "Cloudflare-v4",
      "ipv4": "1.1.1.1",
      "dport": 443
    },
    {
      "name": "Cloudflare-v6",
      "ipv6": "2606:4700:4700::1111",
      "dport": 443
    }
  ]
}
```
### Config File Example (Minimal)
```bash
{
  "ipv4": "1.1.1.1",
  "ipv6": "",
  "dport": 443,
  "webhook": "https://discord.com/api/webhooks/YOUR_WEBHOOK",
  "hostname": "",
  "delay": 1,
  "timeout": 5,
  "timezone": "Europe/Berlin"
}
```
