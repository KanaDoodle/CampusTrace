#!/bin/sh
# Owns disposable Kafka/OpenSearch plus the adversarial harness's SQL/Redis/etcd.
# No .env, existing application volumes, or external model provider is used.
set -eu
cd "$(dirname "$0")/.."
kafka_name="ct-kafka-validation-$$"
search_name="ct-kafka-search-validation-$$"
evidence="$PWD/bin/verification/kafka-20261031"
mkdir -p "$evidence"
cleanup(){ docker rm -f "$kafka_name" "$search_name" >/dev/null 2>&1 || true; }
trap cleanup EXIT HUP INT TERM
export GOWORK=off GOTOOLCHAIN=local
# The extra loopback listener is only for host-run acceptance tests. Normal
# compose publishes no Kafka or OpenSearch host port.
kafka_port=$(python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1]);s.close()')
docker run --rm -d --name "$kafka_name" --memory=1024m --tmpfs /var/lib/kafka/data:uid=1000,gid=1000 -p "127.0.0.1:$kafka_port:19092" \
 -e CLUSTER_ID=MkU3OEVBNTcwNTJENDM2Qk -e KAFKA_NODE_ID=1 -e KAFKA_PROCESS_ROLES=broker,controller \
 -e KAFKA_LISTENERS=INTERNAL://:9092,EXTERNAL://:19092,CONTROLLER://:9093 \
 -e "KAFKA_ADVERTISED_LISTENERS=INTERNAL://localhost:9092,EXTERNAL://127.0.0.1:$kafka_port" \
 -e KAFKA_LISTENER_SECURITY_PROTOCOL_MAP=INTERNAL:PLAINTEXT,EXTERNAL:PLAINTEXT,CONTROLLER:PLAINTEXT \
 -e KAFKA_CONTROLLER_LISTENER_NAMES=CONTROLLER -e KAFKA_CONTROLLER_QUORUM_VOTERS=1@localhost:9093 \
 -e KAFKA_INTER_BROKER_LISTENER_NAME=INTERNAL -e KAFKA_OFFSETS_TOPIC_REPLICATION_FACTOR=1 \
 -e KAFKA_TRANSACTION_STATE_LOG_REPLICATION_FACTOR=1 -e KAFKA_TRANSACTION_STATE_LOG_MIN_ISR=1 \
 -e KAFKA_GROUP_INITIAL_REBALANCE_DELAY_MS=0 -e KAFKA_AUTO_CREATE_TOPICS_ENABLE=false \
 -e KAFKA_LOG_DIRS=/var/lib/kafka/data -e 'KAFKA_HEAP_OPTS=-Xms256m -Xmx512m' apache/kafka:4.4.0 >/dev/null
docker run --rm -d --name "$search_name" --memory=1536m --tmpfs /usr/share/opensearch/data:uid=1000,gid=1000 -p 127.0.0.1::9200 \
 -e discovery.type=single-node -e DISABLE_SECURITY_PLUGIN=true -e DISABLE_INSTALL_DEMO_CONFIG=true \
 -e 'OPENSEARCH_JAVA_OPTS=-Xms512m -Xmx512m' opensearchproject/opensearch:3.9.0 >/dev/null
attempt=0
until docker exec "$kafka_name" /opt/kafka/bin/kafka-topics.sh --bootstrap-server localhost:9092 --list >/dev/null 2>&1 && docker exec "$search_name" curl -fsS 'http://127.0.0.1:9200/_cluster/health?wait_for_status=yellow&timeout=1s' >/dev/null 2>&1;do
 attempt=$((attempt+1));if [ "$attempt" -ge 90 ];then docker logs --tail 30 "$kafka_name";docker logs --tail 30 "$search_name";exit 1;fi;sleep 1
done
export KAFKA_TEST_BROKERS="127.0.0.1:$kafka_port" OPENSEARCH_TEST_URL="http://$(docker port "$search_name" 9200/tcp)"
go test -race -count=1 -v ./internal/jobevent ./internal/eventbus
ADVERSARIAL_TESTS='^TestKafka' sh scripts/adversarial-local.sh
