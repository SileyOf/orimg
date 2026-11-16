#!/usr/bin/env bash
# Read-only pre-flight check for moving a v0.4.x binary site into Docker.
#
#   sudo bash migrate-check.sh /path/to/old/site [/path/to/new/compose/dir]
#
# It reads the old .env, data directory and database settings, and reports
# what the container needs. It never writes files, never changes the database
# and never prints passwords or keys.
set -uo pipefail

OLD="${1:-}"
NEW="${2:-$PWD}"
if [[ -z "$OLD" || ! -d "$OLD" ]]; then
  echo "usage: bash migrate-check.sh <old-site-dir> [new-compose-dir]" >&2
  exit 2
fi
OLD="$(cd "$OLD" && pwd)"
warn=0
ok()   { printf '  [ok]   %s\n' "$*"; }
info() { printf '  [info] %s\n' "$*"; }
todo() { printf '  [todo] %s\n' "$*"; warn=$((warn + 1)); }

# Same rules as the application's .env loader: first "=" splits, both sides
# trimmed, quote characters stripped from both ends, no "$" expansion.
declare -A ENV=()
if [[ -f "$OLD/.env" ]]; then
  while IFS= read -r line || [[ -n "$line" ]]; do
    line="${line#"${line%%[![:space:]]*}"}"; line="${line%"${line##*[![:space:]]}"}"
    [[ -z "$line" || "$line" == \#* || "$line" != *=* ]] && continue
    key="${line%%=*}"; value="${line#*=}"
    key="${key%"${key##*[![:space:]]}"}"
    value="${value#"${value%%[![:space:]]*}"}"
    while [[ "$value" == [\"\']* ]]; do value="${value:1}"; done
    while [[ "$value" == *[\"\'] ]]; do value="${value:0:${#value}-1}"; done
    [[ -n "$key" ]] && ENV["$key"]="$value"
  done < "$OLD/.env"
fi
get() { local v="${ENV[$1]-}"; [[ -n "$v" ]] && printf '%s' "$v" || printf '%s' "$2"; }

echo "== Old site: $OLD"
[[ -x "$OLD/zhuapic" ]] && ok "binary found: zhuapic" || info "no zhuapic binary in this directory"
if [[ -f "$OLD/.env" ]]; then
  ok ".env found ($(stat -c '%a %U' "$OLD/.env" 2>/dev/null || echo '?'))"
else
  todo "no .env: if the site uses data/installation.json, copy data/ only and use compose.yaml-style install file instead of legacy.env"
fi

echo "== Settings from .env (secrets hidden)"
for key in PORT BASE_URL DB_HOST DB_PORT DB_NAME DB_USER DB_TLS TRUSTED_PROXIES STORAGE_MODE MAX_UPLOAD_MB INSTALL_CONFIG UPLOAD_DIR CACHE_DIR ASSET_DIR DISABLE_BACKGROUND_WORK; do
  [[ -n "${ENV[$key]+x}" ]] && info "$key=${ENV[$key]}"
done
for key in DB_PASSWORD JWT_SECRET S3_SECRET_KEY ADMIN_PASSWORD; do
  if [[ -n "${ENV[$key]+x}" ]]; then
    v="${ENV[$key]}"; note="set, ${#v} chars"
    [[ "$v" == *'$'* ]] && note="$note, contains \$ (fine: legacy.env is mounted, not passed through Compose)"
    info "$key: $note"
  fi
done
jwt="${ENV[JWT_SECRET]-}"
if [[ -n "$jwt" && ${#jwt} -lt 32 ]]; then
  info "JWT_SECRET is shorter than 32 chars: the separate session key file must move with data/"
fi
port="$(get PORT 8080)"
info "old listen port: $port  ->  set ORIGIN_PORT=$port in the new compose .env"
case "$(get DB_HOST 127.0.0.1)" in
  127.0.0.1|localhost|::1) ok "database on this host: compose.legacy.yaml (host network) keeps DB_HOST unchanged" ;;
  *) info "database host $(get DB_HOST ''): make sure it accepts connections from this server as before" ;;
esac

echo "== Files that must move"
resolve() { [[ "$1" == /* ]] && printf '%s' "$1" || printf '%s/%s' "$OLD" "$1"; }
install_cfg="$(resolve "$(get INSTALL_CONFIG data/installation.json)")"
[[ -f "$install_cfg" ]] && info "installation file: $install_cfg"
key_file="$install_cfg.session-key"
if [[ -f "$key_file" ]]; then
  mode="$(stat -c '%a' "$key_file" 2>/dev/null || echo '?')"
  ok "session key: $key_file (mode $mode) - must be copied with data/ and stay 0600"
elif [[ -n "$jwt" && ${#jwt} -lt 32 ]]; then
  todo "session key file $key_file not found although JWT_SECRET is short; existing logins will be reset (no data loss)"
fi
for item in UPLOAD_DIR:data/uploads CACHE_DIR:data/cache ASSET_DIR:data/site-assets; do
  key="${item%%:*}"; def="${item#*:}"; raw="$(get "$key" "$def")"; dir="$(resolve "$raw")"
  if [[ "$raw" == /* && "$raw" != "$OLD/data"* ]]; then
    todo "$key is an absolute path outside data/: mount $raw at the same path in compose.legacy.yaml"
  fi
  if [[ -d "$dir" ]]; then info "$key $dir: $(du -sh "$dir" 2>/dev/null | cut -f1)"; else info "$key $dir: not present"; fi
done
if compgen -G "$OLD/static/assets/site-*" >/dev/null; then
  for f in "$OLD"/static/assets/site-*; do
    b="$(basename "$f")"
    if [[ -f "$(resolve "$(get ASSET_DIR data/site-assets)")/$b" ]]; then ok "custom asset already in site-assets: $b"
    else todo "custom asset only in static/: copy $b into data/site-assets/"; fi
  done
fi
if [[ -d "$OLD/data" ]]; then
  info "data/ total: $(du -sh "$OLD/data" 2>/dev/null | cut -f1)  (owner: $(stat -c '%U:%G' "$OLD/data" 2>/dev/null))"
  mkdir_target="$NEW"; [[ -d "$mkdir_target" ]] || mkdir_target="$(dirname "$NEW")"
  info "free space at $mkdir_target: $(df -h "$mkdir_target" 2>/dev/null | awk 'NR==2 {print $4}')"
fi

echo "== Database"
if ! command -v mysql >/dev/null 2>&1; then
  todo "mysql client not found; skipped database checks (run on the MySQL host or install the client)"
else
  q() {
    MYSQL_PWD="${ENV[DB_PASSWORD]-}" mysql --protocol=TCP -h "$(get DB_HOST 127.0.0.1)" -P "$(get DB_PORT 3306)" \
      -u "$(get DB_USER root)" -N -B -e "$1" "$(get DB_NAME zhuapic)" 2>/dev/null
  }
  if ! q "SELECT 1" >/dev/null; then
    todo "cannot connect with the credentials in .env (checks skipped)"
  else
    ok "connected to $(get DB_NAME zhuapic) on $(get DB_HOST 127.0.0.1):$(get DB_PORT 3306)"
    schema="$(q "SELECT COALESCE(MAX(version),0) FROM schema_migrations")"
    if [[ "$schema" == 9 ]]; then ok "schema version 9: same as v0.5.x, rollback to the binary stays possible"
    else todo "schema version ${schema:-unknown}: v0.5.x will migrate it to 9; keep the database backup for rollback"; fi
    info "users: $(q 'SELECT COUNT(*) FROM users')  images: $(q 'SELECT COUNT(*) FROM images')"
    while IFS=$'\t' read -r key value; do
      [[ -z "$key" ]] && continue
      case "$key" in
        mysqldump_path)
          [[ "$value" == /usr/bin/mysqldump || "$value" == mysqldump ]] && ok "mysqldump_path=$value" ||
            info "mysqldump_path=$value does not exist in the image; v0.5.2+ falls back to /usr/bin/mysqldump" ;;
        *)
          if [[ "$value" == /* && "$value" != "$OLD/data"* ]]; then todo "setting $key=$value: mount this path at the same location"
          elif [[ "$value" == "$OLD/data"* ]]; then todo "setting $key=$value: absolute path inside the old site; also mount ./data at $OLD/data or change it to a data/... path"
          else ok "setting $key=$value"; fi ;;
      esac
    done < <(q "SELECT \`key\`, value FROM site_settings WHERE \`key\` IN ('local_upload_dir','s3_cache_dir','thumb_dir','mysqldump_path') AND value <> ''")
    while IFS=$'\t' read -r id name dir; do
      [[ -z "$id" || -z "$dir" || "$dir" == NULL ]] && continue
      if [[ "$dir" == /* && "$dir" != "$OLD/data"* ]]; then todo "storage location #$id ($name) directory $dir: mount it at the same path"
      elif [[ "$dir" == "$OLD/data"* ]]; then todo "storage location #$id ($name) directory $dir: absolute path inside the old site; mount ./data at $OLD/data as well"
      else ok "storage location #$id ($name) directory $dir"; fi
    done < <(q "SELECT id, name, JSON_UNQUOTE(JSON_EXTRACT(config_json,'$.directory')) FROM storage_targets WHERE provider='local'" 2>/dev/null)
  fi
fi

echo "== Docker"
if command -v docker >/dev/null 2>&1; then
  ok "$(docker --version 2>/dev/null)"
  docker compose version >/dev/null 2>&1 && ok "$(docker compose version 2>/dev/null)" || todo "docker compose plugin missing"
else
  todo "docker is not installed"
fi

echo
if [[ $warn -eq 0 ]]; then echo "No blocking items found."; else echo "$warn item(s) marked [todo] need attention before the switch."; fi
