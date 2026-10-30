#!/bin/sh
# Owns only disposable synthetic dependencies. Never reads .env or user volumes.
set -eu
cd "$(dirname "$0")/.."
search_name="ct-search-validation-$$"
cleanup(){ docker rm -f "$search_name" >/dev/null 2>&1 || true; }
trap cleanup EXIT HUP INT TERM
mkdir -p bin/verification/search-20261030
export GOWORK=off GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local
# Temporary loopback publication is for this acceptance only, never normal compose.
docker run --rm -d --name "$search_name" --memory=1536m --tmpfs /usr/share/opensearch/data:uid=1000,gid=1000 -p 127.0.0.1::9200 -e discovery.type=single-node -e DISABLE_SECURITY_PLUGIN=true -e DISABLE_INSTALL_DEMO_CONFIG=true -e 'OPENSEARCH_JAVA_OPTS=-Xms512m -Xmx512m' opensearchproject/opensearch:3.9.0 >/dev/null
attempt=0
until docker exec "$search_name" curl -fsS 'http://127.0.0.1:9200/_cluster/health?wait_for_status=yellow&timeout=1s' >/dev/null 2>&1;do
 attempt=$((attempt+1));if [ "$attempt" -ge 90 ];then docker logs --tail 30 "$search_name";exit 1;fi;sleep 1
done
export OPENSEARCH_TEST_URL="http://$(docker port "$search_name" 9200/tcp)"
go test -race -count=1 -v ./internal/searchapi ./internal/searchservice
ADVERSARIAL_TESTS="${SEARCH_TESTS:-^TestSearch}" sh scripts/adversarial-local.sh
