#!/usr/bin/env bash
# Round-robin (interleaved) IP rate-limiting smoke test with Redis FLUSHDB cleanup.

set -euo pipefail

SERVER_URL="${SERVER_URL:-http://localhost:8080/protected}"
REDIS_CLI="${REDIS_CLI:-docker exec -i rate-limiter-redis redis-cli}"
IP_LIST=("1.1.1.1" "2.2.2.2" "3.3.3.3")
REQUESTS_PER_IP="${REQUESTS_PER_IP:-50}"
SLEEP_BETWEEN_REQUESTS="${SLEEP_BETWEEN_REQUESTS:-0.05}"

echo "Rate Limiter Round-Robin IP Test"
echo "Server: $SERVER_URL"
echo "IPs: ${IP_LIST[*]}"
echo "Rounds per IP: $REQUESTS_PER_IP"
echo "----------------------------------------"

echo "Flushing Redis database (FLUSHDB)..."
$REDIS_CLI FLUSHDB >/dev/null
echo "Redis flushed successfully."
echo "----------------------------------------"

for ((round = 1; round <= REQUESTS_PER_IP; round++)); do
  echo ""
  echo "Round #$round"
  for ip in "${IP_LIST[@]}"; do
    http_code=$(curl -s -o /dev/null -w "%{http_code}" -H "X-Forwarded-For: $ip" "$SERVER_URL" || true)

    if [[ "$http_code" == "200" ]]; then
      printf "OK   [%s] round #%d -> 200 OK" "$ip" "$round"
    elif [[ "$http_code" == "429" ]]; then
      printf "LIM  [%s] round #%d -> 429 TOO MANY REQUESTS" "$ip" "$round"
    else
      printf "ERR  [%s] round #%d -> HTTP %s" "$ip" "$round" "$http_code"
    fi

    cnt=$($REDIS_CLI GET "ip:${ip}" 2>/dev/null || true)
    ttl=$($REDIS_CLI TTL "ip:${ip}" 2>/dev/null || echo "-2")
    blk_ttl=$($REDIS_CLI TTL "ip:${ip}:blocked" 2>/dev/null || echo "-2")
    echo " | count=${cnt:-0} ttl=${ttl} blocked_ttl=${blk_ttl}"

    sleep "$SLEEP_BETWEEN_REQUESTS"
  done
done

echo ""
echo "----------------------------------------"
echo "Final Redis state (counters and blocks):"
for ip in "${IP_LIST[@]}"; do
  cnt=$($REDIS_CLI GET "ip:${ip}" 2>/dev/null || true)
  blk=$($REDIS_CLI GET "ip:${ip}:blocked" 2>/dev/null || true)
  blk_ttl=$($REDIS_CLI TTL "ip:${ip}:blocked" 2>/dev/null || echo "-2")
  ttl=$($REDIS_CLI TTL "ip:${ip}" 2>/dev/null || echo "-2")
  echo "IP: $ip | count=${cnt:-0} | ttl=${ttl} | blocked=${blk:-no} | blocked_ttl=${blk_ttl}"
done

echo ""
echo "----------------------------------------"
echo "Flushing Redis database (FLUSHDB)..."
$REDIS_CLI FLUSHDB >/dev/null
echo "Test completed and Redis cleaned."
