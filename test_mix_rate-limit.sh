#!/usr/bin/env bash
# test_mix_rate-limit.sh
# Teste completo do rate limiter por IP e Token, incluindo bloqueios

set -euo pipefail

SERVER_URL="${SERVER_URL:-http://localhost:8080/protected}"
API_KEY_URL="${API_KEY_URL:-http://localhost:8080/api-key}"
REDIS_CLI="${REDIS_CLI:-docker exec -i rate-limiter-redis redis-cli}"
IP_LIST=("1.1.1.1" "2.2.2.2" "3.3.3.3")
REQUESTS_PER_IP="${REQUESTS_PER_IP:-20}"
REQUESTS_PER_TOKEN="${REQUESTS_PER_TOKEN:-40}"
SLEEP_BETWEEN_REQUESTS="${SLEEP_BETWEEN_REQUESTS:-0.5}"
BLOCKED_WAIT_SECONDS=35   # Tempo para testar desbloqueio automático

echo "🚀 Teste Rate Limiter Completo"
echo "Server: $SERVER_URL"
echo "IPs: ${IP_LIST[*]}"
echo "Requests per IP: $REQUESTS_PER_IP"
echo "Requests per Token: $REQUESTS_PER_TOKEN"
echo "----------------------------------------"

# Limpar Redis antes do teste
echo "🧹 Limpando Redis..."
$REDIS_CLI FLUSHDB >/dev/null
echo "Redis limpo!"
echo "----------------------------------------"

# Criar token dinamicamente
TOKEN_JSON=$(curl -s -X POST "$API_KEY_URL" \
  -H 'Content-Type: application/json' \
  -d '{"time_window": 5, "max_requests": 5, "blocked_duration": 10, "token_ttl": 3600}')
TOKEN=$(echo "$TOKEN_JSON" | sed -n 's/.*"token":"\([^"]*\)".*/\1/p')
echo "Token criado: $TOKEN"
echo "----------------------------------------"

# 1️⃣ Teste por IP individual (até limite e desbloqueio)
echo -e "\n🔹 Teste por IP individual (até limite e desbloqueio)"
for ip in "${IP_LIST[@]}"; do
  echo -e "\n🌐 Testando IP: $ip"
  for ((i=1; i<=REQUESTS_PER_IP+2; i++)); do
    HTTP_STATUS=$(curl -s -o /dev/null -w "%{http_code}" -H "X-Forwarded-For: $ip" "$SERVER_URL")
    COUNT=$($REDIS_CLI GET "ip:${ip}" 2>/dev/null || echo "0")
    TTL=$($REDIS_CLI TTL "ip:${ip}" 2>/dev/null || echo "-2")
    BLOCKED_TTL=$($REDIS_CLI TTL "ip:${ip}:blocked" 2>/dev/null || echo "-2")
    echo "Req #$i | Status: $HTTP_STATUS | count TTL: $TTL | blocked TTL: $BLOCKED_TTL"
  done
done

# 2️⃣ Teste intercalado IPs (round-robin)
echo -e "\n🔹 Teste intercalado IPs (round-robin)"
for ((round=1; round<=REQUESTS_PER_IP+2; round++)); do
  echo -e "\n🔁 Round #$round"
  for ip in "${IP_LIST[@]}"; do
    HTTP_STATUS=$(curl -s -o /dev/null -w "%{http_code}" -H "X-Forwarded-For: $ip" "$SERVER_URL")
    COUNT=$($REDIS_CLI GET "ip:${ip}" 2>/dev/null || echo "0")
    TTL=$($REDIS_CLI TTL "ip:${ip}" 2>/dev/null || echo "-2")
    BLOCKED_TTL=$($REDIS_CLI TTL "ip:${ip}:blocked" 2>/dev/null || echo "-2")
    echo "[$ip] Status: $HTTP_STATUS | TTL: $TTL | blocked TTL: $BLOCKED_TTL"
  done
  sleep "$SLEEP_BETWEEN_REQUESTS"
done

# 3️⃣ Teste por Token (até limite)
echo -e "\n🔹 Teste por Token (até limite)"
for ((i=1; i<=REQUESTS_PER_TOKEN; i++)); do
  HTTP_STATUS=$(curl -s -o /dev/null -w "%{http_code}" -H "API_KEY: $TOKEN" "$SERVER_URL")
  COUNT=$($REDIS_CLI GET "$TOKEN" 2>/dev/null || echo "0")
  TTL=$($REDIS_CLI TTL "$TOKEN" 2>/dev/null || echo "-2")
  BLOCKED_TTL=$($REDIS_CLI TTL "${TOKEN}:blocked" 2>/dev/null || echo "-2")
  echo "Req #$i | Status: $HTTP_STATUS | token TTL: $TTL | blocked TTL: $BLOCKED_TTL"
done

# 4️⃣ Teste IP + Token (token deve prevalecer)
echo -e "\n🔹 Teste IP + Token (token deve prevalecer)"
for ((i=1; i<=REQUESTS_PER_TOKEN; i++)); do
  for ip in "${IP_LIST[@]}"; do
    HTTP_STATUS=$(curl -s -o /dev/null -w "%{http_code}" -H "X-Forwarded-For: $ip" -H "API_KEY: $TOKEN" "$SERVER_URL")
    COUNT_IP=$($REDIS_CLI GET "ip:${ip}" 2>/dev/null || echo "0")
    TTL_IP=$($REDIS_CLI TTL "ip:${ip}" 2>/dev/null || echo "-2")
    BLOCKED_TTL_IP=$($REDIS_CLI TTL "ip:${ip}:blocked" 2>/dev/null || echo "-2")
    COUNT_TOKEN=$($REDIS_CLI GET "$TOKEN" 2>/dev/null || echo "0")
    TTL_TOKEN=$($REDIS_CLI TTL "$TOKEN" 2>/dev/null || echo "-2")
    BLOCKED_TTL_TOKEN=$($REDIS_CLI TTL "${TOKEN}:blocked" 2>/dev/null || echo "-2")
    echo "[$ip] Req #$i | Status: $HTTP_STATUS | token TTL: $TTL_TOKEN | blocked TTL: $BLOCKED_TTL_TOKEN"
  done
done

# 5️⃣ Teste desbloqueio automático
echo -e "\n🔹 Teste desbloqueio automático (esperando $BLOCKED_WAIT_SECONDS segundos)"
sleep "$BLOCKED_WAIT_SECONDS"
for ip in "${IP_LIST[@]}"; do
  HTTP_STATUS=$(curl -s -o /dev/null -w "%{http_code}" -H "X-Forwarded-For: $ip" "$SERVER_URL")
  BLOCKED_TTL=$($REDIS_CLI TTL "ip:${ip}:blocked" 2>/dev/null || echo "-2")
  echo "[$ip] Status pós-desbloqueio: $HTTP_STATUS | blocked TTL: $BLOCKED_TTL"
done
HTTP_STATUS=$(curl -s -o /dev/null -w "%{http_code}" -H "API_KEY: $TOKEN" "$SERVER_URL")
BLOCKED_TTL=$($REDIS_CLI TTL "${TOKEN}:blocked" 2>/dev/null || echo "-2")
echo "[TOKEN] Status pós-desbloqueio: $HTTP_STATUS | blocked TTL: $BLOCKED_TTL"

# 6️⃣ Estado final no Redis
echo -e "\n----------------------------------------"
echo "🔎 Estado final no Redis:"
for ip in "${IP_LIST[@]}"; do
  COUNT=$($REDIS_CLI GET "ip:${ip}" 2>/dev/null || echo "")
  BLOCKED=$($REDIS_CLI GET "ip:${ip}:blocked" 2>/dev/null || echo "")
  TTL=$($REDIS_CLI TTL "ip:${ip}" 2>/dev/null || echo "-2")
  BLOCKED_TTL=$($REDIS_CLI TTL "ip:${ip}:blocked" 2>/dev/null || echo "-2")
  echo "IP: $ip | count: $COUNT | TTL: $TTL | blocked: $BLOCKED | blocked TTL: $BLOCKED_TTL"
done
COUNT=$($REDIS_CLI GET "$TOKEN" 2>/dev/null || echo "")
BLOCKED=$($REDIS_CLI GET "${TOKEN}:blocked" 2>/dev/null || echo "")
TTL=$($REDIS_CLI TTL "$TOKEN" 2>/dev/null || echo "-2")
BLOCKED_TTL=$($REDIS_CLI TTL "${TOKEN}:blocked" 2>/dev/null || echo "-2")
echo "TOKEN: $TOKEN | count: $COUNT | TTL: $TTL | blocked: $BLOCKED | blocked TTL: $BLOCKED_TTL"

echo -e "\n🧹 Limpando Redis novamente..."
$REDIS_CLI FLUSHDB >/dev/null
echo "✅ Teste completo finalizado e Redis limpo!"
