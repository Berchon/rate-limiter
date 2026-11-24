#!/usr/bin/env bash
# Sequential IP rate-limiting smoke test with Redis FLUSHDB cleanup.

set -euo pipefail

SERVER_URL="${SERVER_URL:-http://localhost:8080/protected}"
REDIS_CLI="${REDIS_CLI:-docker exec -i rate-limiter-redis redis-cli}"
IP_LIST=("1.1.1.1" "2.2.2.2" "3.3.3.3")
REQUESTS_PER_IP="${REQUESTS_PER_IP:-50}"
SLEEP_BETWEEN_REQUESTS="${SLEEP_BETWEEN_REQUESTS:-0.05}"

echo "Rate Limiter Sequential IP Test"
echo "Server: $SERVER_URL"
echo "IPs: ${IP_LIST[*]}"
echo "Requests per IP: $REQUESTS_PER_IP"
echo "----------------------------------------"

echo "Flushing Redis database (FLUSHDB)..."
$REDIS_CLI FLUSHDB >/dev/null
echo "Redis flushed successfully."
echo "----------------------------------------"

for ip in "${IP_LIST[@]}"; do
  echo ""
  echo "Testing IP: $ip"
  for ((i = 1; i <= REQUESTS_PER_IP; i++)); do
    http_code=$(curl -s -o /dev/null -w "%{http_code}" -H "X-Forwarded-For: $ip" "$SERVER_URL" || true)

    if [[ "$http_code" == "200" ]]; then
      printf "OK   [%s] req #%d -> 200 OK" "$ip" "$i"
    elif [[ "$http_code" == "429" ]]; then
      printf "LIM  [%s] req #%d -> 429 TOO MANY REQUESTS" "$ip" "$i"
    else
      printf "ERR  [%s] req #%d -> HTTP %s" "$ip" "$i" "$http_code"
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
