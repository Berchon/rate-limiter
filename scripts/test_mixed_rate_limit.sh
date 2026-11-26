#!/usr/bin/env bash
# End-to-end smoke test covering IP limits, token limits, token precedence over IP, and automatic unblock.

set -euo pipefail

SERVER_URL="${SERVER_URL:-http://localhost:8080/protected}"
API_KEY_URL="${API_KEY_URL:-http://localhost:8080/api-key}"
REDIS_CLI="${REDIS_CLI:-docker exec -i rate-limiter-redis redis-cli}"
IP_LIST=("1.1.1.1" "2.2.2.2" "3.3.3.3")
REQUESTS_PER_IP="${REQUESTS_PER_IP:-20}"
REQUESTS_PER_TOKEN="${REQUESTS_PER_TOKEN:-40}"
SLEEP_BETWEEN_REQUESTS="${SLEEP_BETWEEN_REQUESTS:-0.5}"
BLOCKED_WAIT_SECONDS="${BLOCKED_WAIT_SECONDS:-35}"

echo "Rate Limiter Full Mixed Test (IP + Token)"
echo "Server: $SERVER_URL"
echo "IPs: ${IP_LIST[*]}"
echo "Requests per IP: $REQUESTS_PER_IP"
echo "Requests per Token: $REQUESTS_PER_TOKEN"
echo "----------------------------------------"

echo "Flushing Redis database (FLUSHDB)..."
$REDIS_CLI FLUSHDB >/dev/null
echo "Redis flushed successfully."
echo "----------------------------------------"

# Dynamically create an API key token
TOKEN_JSON=$(curl -s -X POST "$API_KEY_URL" \
  -H "Content-Type: application/json" \
  -d '{"time_window": 5, "max_requests": 5, "blocked_duration": 10, "token_ttl": 3600}')
TOKEN=$(echo "$TOKEN_JSON" | sed -n 's/.*"token":"\([^"]*\)".*/\1/p')
if [[ -z "$TOKEN" ]]; then
  echo "Failed to create API token. Response: $TOKEN_JSON" >&2
  exit 1
fi
echo "Created API token: $TOKEN"
echo "----------------------------------------"

# 1. Individual IP test (until limit and block)
echo ""
echo "[1/5] Testing individual IPs (until rate limit and block)"
for ip in "${IP_LIST[@]}"; do
  echo ""
  echo "Testing IP: $ip"
  for ((i = 1; i <= REQUESTS_PER_IP + 2; i++)); do
    http_status=$(curl -s -o /dev/null -w "%{http_code}" -H "X-Forwarded-For: $ip" "$SERVER_URL" || true)
    ttl=$($REDIS_CLI TTL "ip:${ip}" 2>/dev/null || echo "-2")
    blocked_ttl=$($REDIS_CLI TTL "ip:${ip}:blocked" 2>/dev/null || echo "-2")
    echo "Req #$i | Status: $http_status | count TTL: $ttl | blocked TTL: $blocked_ttl"
  done
done

# 2. Interleaved IP test (round-robin)
echo ""
echo "[2/5] Testing interleaved IPs (round-robin)"
for ((round = 1; round <= REQUESTS_PER_IP + 2; round++)); do
  echo ""
  echo "Round #$round"
  for ip in "${IP_LIST[@]}"; do
    http_status=$(curl -s -o /dev/null -w "%{http_code}" -H "X-Forwarded-For: $ip" "$SERVER_URL" || true)
    ttl=$($REDIS_CLI TTL "ip:${ip}" 2>/dev/null || echo "-2")
    blocked_ttl=$($REDIS_CLI TTL "ip:${ip}:blocked" 2>/dev/null || echo "-2")
    echo "[$ip] Status: $http_status | TTL: $ttl | blocked TTL: $blocked_ttl"
  done
  sleep "$SLEEP_BETWEEN_REQUESTS"
done

# 3. Token-only test (until limit)
echo ""
echo "[3/5] Testing token rate limit"
for ((i = 1; i <= REQUESTS_PER_TOKEN; i++)); do
  http_status=$(curl -s -o /dev/null -w "%{http_code}" -H "API_KEY: $TOKEN" "$SERVER_URL" || true)
  ttl=$($REDIS_CLI TTL "$TOKEN" 2>/dev/null || echo "-2")
  blocked_ttl=$($REDIS_CLI TTL "${TOKEN}:blocked" 2>/dev/null || echo "-2")
  echo "Req #$i | Status: $http_status | token TTL: $ttl | blocked TTL: $blocked_ttl"
done

# 4. IP + Token test (token rules must take precedence over IP rules)
echo ""
echo "[4/5] Testing IP + Token (token rules take precedence)"
for ((i = 1; i <= REQUESTS_PER_TOKEN; i++)); do
  for ip in "${IP_LIST[@]}"; do
    http_status=$(curl -s -o /dev/null -w "%{http_code}" -H "X-Forwarded-For: $ip" -H "API_KEY: $TOKEN" "$SERVER_URL" || true)
    ttl_token=$($REDIS_CLI TTL "$TOKEN" 2>/dev/null || echo "-2")
    blocked_ttl_token=$($REDIS_CLI TTL "${TOKEN}:blocked" 2>/dev/null || echo "-2")
    echo "[$ip] Req #$i | Status: $http_status | token TTL: $ttl_token | blocked TTL: $blocked_ttl_token"
  done
done

# 5. Automatic unblock after waiting
echo ""
echo "[5/5] Testing automatic unblock (waiting ${BLOCKED_WAIT_SECONDS}s)"
sleep "$BLOCKED_WAIT_SECONDS"
for ip in "${IP_LIST[@]}"; do
  http_status=$(curl -s -o /dev/null -w "%{http_code}" -H "X-Forwarded-For: $ip" "$SERVER_URL" || true)
  blocked_ttl=$($REDIS_CLI TTL "ip:${ip}:blocked" 2>/dev/null || echo "-2")
  echo "[$ip] Post-unblock status: $http_status | blocked TTL: $blocked_ttl"
done
http_status=$(curl -s -o /dev/null -w "%{http_code}" -H "API_KEY: $TOKEN" "$SERVER_URL" || true)
blocked_ttl=$($REDIS_CLI TTL "${TOKEN}:blocked" 2>/dev/null || echo "-2")
echo "[TOKEN] Post-unblock status: $http_status | blocked TTL: $blocked_ttl"

echo ""
echo "----------------------------------------"
echo "Final Redis state:"
for ip in "${IP_LIST[@]}"; do
  count=$($REDIS_CLI GET "ip:${ip}" 2>/dev/null || true)
  blocked=$($REDIS_CLI GET "ip:${ip}:blocked" 2>/dev/null || true)
  ttl=$($REDIS_CLI TTL "ip:${ip}" 2>/dev/null || echo "-2")
  blocked_ttl=$($REDIS_CLI TTL "ip:${ip}:blocked" 2>/dev/null || echo "-2")
  echo "IP: $ip | count: ${count:-0} | TTL: $ttl | blocked: ${blocked:-no} | blocked TTL: $blocked_ttl"
done
count=$($REDIS_CLI GET "$TOKEN" 2>/dev/null || true)
blocked=$($REDIS_CLI GET "${TOKEN}:blocked" 2>/dev/null || true)
ttl=$($REDIS_CLI TTL "$TOKEN" 2>/dev/null || echo "-2")
blocked_ttl=$($REDIS_CLI TTL "${TOKEN}:blocked" 2>/dev/null || echo "-2")
echo "TOKEN: $TOKEN | count: ${count:-0} | TTL: $ttl | blocked: ${blocked:-no} | blocked TTL: $blocked_ttl"

echo ""
echo "Flushing Redis database (FLUSHDB)..."
$REDIS_CLI FLUSHDB >/dev/null
echo "Full mixed test completed and Redis cleaned."
