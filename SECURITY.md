# Security policy

Please report vulnerabilities privately to **support@oddomens.com** or through a GitHub security advisory, not in a public issue. Include steps to reproduce and the version (`/coffer version`, or **Admin → Settings**). We aim to reply within 3 working days.

## Deployment checklist

- [ ] Only port **8081** (share links) is reachable from the internet; 8080 is LAN/VPN only, or restricted with `COFFER_APP_NETWORKS`.
- [ ] Port 445 (SMB) is never forwarded to the internet.
- [ ] HTTPS terminates at your tunnel or proxy. The bundled tunnel is trusted by its fixed address; any other proxy's exact address (not a Docker subnet) is in `COFFER_TRUSTED_PROXIES`, or `COFFER_APP_TRUSTED_PROXIES` for one in front of 8080.
- [ ] The first admin password is changed and two-factor is on for every admin.
- [ ] `COFFER_DATA_DIR` (database + `secret.key`) is backed up and readable only by the Coffer user.
- [ ] Backup folders (`.backups/…` by default) are outside every user's home and shared folders. They hold everyone's files, and a copy of Coffer's database and key.
- [ ] Off-site buckets use **Encrypt files**, and each encrypted drive's recovery key is saved somewhere other than the server.
- [ ] SMB passwords in `samba/users.conf` are long and unique; the file is `chmod 600`.
- [ ] Consider **Admin settings → App settings → Require a password on every link** and a maximum link expiry.
