#!/bin/sh
# Creates the "coffer" owner (matching Coffer's PUID/PGID) and one Samba login
# per line of /etc/coffer/users.conf ("username:password"), then runs smbd.
set -eu

PUID="${PUID:-1000}"
PGID="${PGID:-1000}"
SMB_SHARE_NAME="${SMB_SHARE_NAME:-Files}"
SMB_ENCRYPT="${SMB_ENCRYPT:-if_required}"
# Private, link-local, Tailscale (100.64/10) and loopback addresses.
SMB_HOSTS_ALLOW="${SMB_HOSTS_ALLOW:-127.0.0.0/8 10.0.0.0/8 172.16.0.0/12 192.168.0.0/16 100.64.0.0/10 169.254.0.0/16 ::1 fc00::/7 fe80::/10}"
USERS_FILE=/etc/coffer/users.conf

case "$SMB_ENCRYPT" in
  if_required|desired|required) ;;
  *) echo "SMB_ENCRYPT must be if_required, desired or required" >&2; exit 1 ;;
esac
case "$SMB_SHARE_NAME" in
  *[!A-Za-z0-9_-]*|"") echo "SMB_SHARE_NAME may only contain letters, digits, - and _" >&2; exit 1 ;;
esac
case "$SMB_HOSTS_ALLOW" in
  *[!0-9A-Fa-f.:/\ ,]*|"") echo "SMB_HOSTS_ALLOW must list addresses or networks, such as 192.168.1.0/24" >&2; exit 1 ;;
esac

getent group coffer >/dev/null || addgroup -g "$PGID" coffer
getent passwd coffer >/dev/null || adduser -D -H -u "$PUID" -G coffer -s /sbin/nologin coffer

sed -e "s/\${SMB_ENCRYPT}/$SMB_ENCRYPT/" -e "s|\${SMB_HOSTS_ALLOW}|$SMB_HOSTS_ALLOW|" /etc/coffer/smb.conf.template > /etc/samba/smb.conf

# One share per drive: /share (the main drive) and every folder mounted
# under /shares/<Name> (extra local drives).
add_share() {
  sed -e "s|\${SHARE}|$1|" -e "s|\${PATH_}|$2|" /etc/coffer/share.conf.template >> /etc/samba/smb.conf
  echo "Samba: sharing $2 as \\\\$1"
}
[ -d /share ] && add_share "$SMB_SHARE_NAME" /share
if [ -d /shares ]; then
  for d in /shares/*/; do
    [ -d "$d" ] || continue
    name=$(basename "$d")
    case "$name" in *[!A-Za-z0-9_-]*) echo "skipping /shares/$name: use letters, digits, - and _" >&2; continue ;; esac
    add_share "$name" "${d%/}"
  done
fi

if [ ! -s "$USERS_FILE" ]; then
  echo "No users in $USERS_FILE; add lines like 'editor:a-long-password'." >&2
  exit 1
fi

# Rebuild the password database from the file on every start.
rm -f /var/lib/samba/private/passdb.tdb
count=0
while IFS= read -r line || [ -n "$line" ]; do
  case "$line" in ''|\#*) continue ;; esac
  user="${line%%:*}"
  pass="${line#*:}"
  case "$user" in
    *[!a-z0-9._-]*|"") echo "skipping invalid username '$user'" >&2; continue ;;
  esac
  case "$pass" in
    change-me*) echo "password for '$user' is still the example one; set your own in users.conf" >&2; exit 1 ;;
  esac
  if [ "${#pass}" -lt 12 ]; then
    echo "password for '$user' is shorter than 12 characters; refusing" >&2
    exit 1
  fi
  getent passwd "$user" >/dev/null || adduser -D -H -G coffer -s /sbin/nologin "$user"
  printf '%s\n%s\n' "$pass" "$pass" | smbpasswd -s -a "$user" >/dev/null
  count=$((count + 1))
done < "$USERS_FILE"
echo "Samba: ready for $count user(s), encryption=$SMB_ENCRYPT"

testparm -s /etc/samba/smb.conf >/dev/null

# Tell Coffer which files are open over SMB, so the web app can show
# "open in After Effects by editor" next to them.
if [ -d /status ]; then
  (
    while :; do
      smbstatus --json >/status/.status.json 2>/dev/null && chmod 0644 /status/.status.json && mv /status/.status.json /status/status.json
      sleep 5
    done
  ) &
fi

exec smbd --foreground --no-process-group --debug-stdout
