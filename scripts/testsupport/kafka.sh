#!/bin/sh
# Starts only an explicitly named disposable broker. Caller owns cleanup.
set -eu
name=$1
case "$name" in ct-*|campustrace-journey-*) ;; *) echo 'test Kafka requires an owned test name' >&2;exit 2;; esac
port=$(python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1]);s.close()')
docker run --rm -d --name "$name" --memory=1024m --tmpfs /var/lib/kafka/data:uid=1000,gid=1000 -p "127.0.0.1:$port:19092" \
 -e CLUSTER_ID=MkU3OEVBNTcwNTJENDM2Qk -e KAFKA_NODE_ID=1 -e KAFKA_PROCESS_ROLES=broker,controller \
 -e KAFKA_LISTENERS=INTERNAL://:9092,EXTERNAL://:19092,CONTROLLER://:9093 \
 -e "KAFKA_ADVERTISED_LISTENERS=INTERNAL://localhost:9092,EXTERNAL://127.0.0.1:$port" \
 -e KAFKA_LISTENER_SECURITY_PROTOCOL_MAP=INTERNAL:PLAINTEXT,EXTERNAL:PLAINTEXT,CONTROLLER:PLAINTEXT \
 -e KAFKA_CONTROLLER_LISTENER_NAMES=CONTROLLER -e KAFKA_CONTROLLER_QUORUM_VOTERS=1@localhost:9093 \
 -e KAFKA_INTER_BROKER_LISTENER_NAME=INTERNAL -e KAFKA_OFFSETS_TOPIC_REPLICATION_FACTOR=1 \
 -e KAFKA_TRANSACTION_STATE_LOG_REPLICATION_FACTOR=1 -e KAFKA_TRANSACTION_STATE_LOG_MIN_ISR=1 \
 -e KAFKA_GROUP_INITIAL_REBALANCE_DELAY_MS=0 -e KAFKA_AUTO_CREATE_TOPICS_ENABLE=false \
 -e KAFKA_LOG_DIRS=/var/lib/kafka/data -e 'KAFKA_HEAP_OPTS=-Xms256m -Xmx512m' apache/kafka:4.4.0 >/dev/null
attempt=0
until docker exec "$name" /opt/kafka/bin/kafka-topics.sh --bootstrap-server localhost:9092 --list >/dev/null 2>&1;do
 attempt=$((attempt+1));if [ "$attempt" -ge 60 ];then docker logs --tail 30 "$name" >&2;exit 1;fi;sleep 1
done
printf '127.0.0.1:%s\n' "$port"
