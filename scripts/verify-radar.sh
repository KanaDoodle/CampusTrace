#!/bin/sh
# Runs the full gated suite against new, exclusively owned disposable schemas.
set -eu
cd "$(dirname "$0")/.."
export GOWORK=off
stamp=$(date +%Y%m%d%H%M%S)_$$
radar_db=campustrace_verify_$stamp
legacy_db=${radar_db}_legacy
upgrade_db=${radar_db}_upgrade
logs=${RADAR_VERIFY_LOGS:-/tmp/campustrace-verify-$stamp}
mkdir -p "$logs"
mysql_admin() { docker compose exec -T mysql mysql -uroot -plocal-root-only "$@"; }
mysql_admin -e "CREATE DATABASE $radar_db; CREATE DATABASE $legacy_db; CREATE DATABASE $upgrade_db; GRANT ALL ON $radar_db.* TO 'campus'@'%'; GRANT ALL ON $legacy_db.* TO 'campus'@'%'; GRANT ALL ON $upgrade_db.* TO 'campus'@'%';" >"$logs/database.log" 2>&1
export CAMPUS_INTEGRATION=1
export MYSQL_TEST_DSN="campus:local-campus-only@tcp(127.0.0.1:13306)/$radar_db?parseTime=true&loc=UTC"
export MYSQL_MIGRATION_TEST_DSN="campus:local-campus-only@tcp(127.0.0.1:13306)/$legacy_db?parseTime=true&loc=UTC"
export MYSQL_RADAR_MIGRATION_TEST_DSN="campus:local-campus-only@tcp(127.0.0.1:13306)/$upgrade_db?parseTime=true&loc=UTC"
set --
if [ "${RADAR_RACE:-0}" = "1" ]; then set -- -race; fi
if go test "$@" -count=1 -v ./... >"$logs/integration.log" 2>&1; then
 printf 'integration exit=0 logs=%s\n' "$logs"
 mysql_admin -e "DROP DATABASE $radar_db; DROP DATABASE $legacy_db; DROP DATABASE $upgrade_db;" >>"$logs/database.log" 2>&1
else
 code=$?
 printf 'integration exit=%s logs=%s schemas retained for diagnosis=%s\n' "$code" "$logs" "$radar_db" >&2
 exit "$code"
fi
