#!/bin/sh
# Owns only this disposable MySQL; never reads .env or an existing Compose stack.
set -eu
cd "$(dirname "$0")/.."
mysql_name="ct-recovery-mysql-$$"
cleanup(){ docker rm -f "$mysql_name" >/dev/null 2>&1 || true; }
trap cleanup EXIT HUP INT TERM
export GOWORK=off GOTOOLCHAIN=local CAMPUS_INTEGRATION=1
export MYSQL_TEST_CONTAINER="$mysql_name"
evidence="$PWD/bin/verification/recovery-epoch-20261031"
mkdir -p "$evidence"
docker run --rm -d --name "$mysql_name" --tmpfs /var/lib/mysql -p 127.0.0.1::3306 -e MYSQL_ROOT_PASSWORD=local-root-only -e MYSQL_DATABASE=ct_recovery_template -e MYSQL_USER=campus -e MYSQL_PASSWORD=local-campus-only mysql:8.4 --mysqlx=OFF >/dev/null
attempt=0
until docker exec "$mysql_name" mysql -h127.0.0.1 -ucampus -plocal-campus-only -e 'SELECT 1' ct_recovery_template >/dev/null 2>&1;do
 attempt=$((attempt+1));if [ "$attempt" -ge 60 ];then echo 'Disposable recovery database not ready' >&2;exit 1;fi;sleep 1
done
mysql_port=$(docker port "$mysql_name" 3306/tcp | sed -n 's/^127.0.0.1://p')
export MYSQL_TEST_DSN="campus:local-campus-only@tcp(127.0.0.1:$mysql_port)/ct_recovery_template?parseTime=true&loc=UTC"
if ! go test -race -count=1 -v ./internal/launcher -run '^TestRecovery' > "$evidence/cli-recovery.log" 2>&1;then
 cat "$evidence/cli-recovery.log" >&2;exit 1
fi
cat "$evidence/cli-recovery.log"
