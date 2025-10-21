#!/usr/bin/env bash
# test_mix_rate-limit.sh
# Teste completo do rate limiter (IPs, Tokens, bloqueios, desbloqueios)

set -euo pipefail

SERVER_URL="${SERVER_URL:-http://localhost:8080/protected}"
API_KEY_URL="${API_KEY_URL:-http://localhost:8080/api-key}"
REDIS_CLI="${REDIS_CLI:-docker exec -i rate-limiter-redis redis-cli}"
IP_LIST=("1.1.1.1" "2.2.2.2" "3.3.3.3")
REQUESTS_PER_IP="${REQUESTS_PER_IP:-40}"
REQUESTS_PER_TOKEN="${REQUESTS_PER_TOKEN:-40}"
SLEEP_BETWEEN_REQUESTS="${SLEEP_BETWEEN_REQUESTS:-0.5}"

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

# Função para obter count e blocked TTL
get_ip_status() {
  ip=$1
  count=$($REDIS_CLI GET "ip:${ip}:count" 2>/dev/null || echo "0")
  blocked_ttl=$($REDIS_CLI TTL "ip:${ip}:blocked" 2>/dev/null || echo "-2")
  echo "$count $blocked_ttl"
}

get_token_status() {
  token=$1
  count=$($REDIS_CLI GET "$token:count" 2>/dev/null || echo "0")
  blocked_ttl=$($REDIS_CLI TTL "$token:blocked" 2>/dev/null || echo "-2")
  echo "$count $blocked_ttl"
}

echo "🔹 Teste por IP individual (até limite e desbloqueio)"
for ip in "${IP_LIST[@]}"; do
  echo -e "\n🌐 Testando IP: $ip"
  for ((i=1; i<=REQUESTS_PER_IP; i++)); do
    http_code=$(curl -s -o /dev/null -w "%{http_code}" -H "X-Forwarded-For: $ip" "$SERVER_URL" || echo "000")
    read count blocked_ttl <<< $(get_ip_status "$ip")
    status_symbol="❌"
    [[ "$http_code" == "200" ]] && status_symbol="✅"
    [[ "$http_code" == "429" ]] && status_symbol="⚠️"
    echo "Req #$i | Status: $http_code $status_symbol | count: $count | blocked TTL: $blocked_ttl"
    sleep "$SLEEP_BETWEEN_REQUESTS"
  done
done

echo "🔹 Teste intercalado IPs (round-robin)"
for ((round=1; round<=REQUESTS_PER_IP; round++)); do
  echo -e "\n🔁 Round #$round"
  for ip in "${IP_LIST[@]}"; do
    http_code=$(curl -s -o /dev/null -w "%{http_code}" -H "X-Forwarded-For: $ip" "$SERVER_URL" || echo "000")
    read count blocked_ttl <<< $(get_ip_status "$ip")
    status_symbol="❌"
    [[ "$http_code" == "200" ]] && status_symbol="✅"
    [[ "$http_code" == "429" ]] && status_symbol="⚠️"
    echo "[$ip] Status: $http_code $status_symbol | count: $count | blocked TTL: $blocked_ttl"
    sleep "$SLEEP_BETWEEN_REQUESTS"
  done
done

echo "🔹 Teste por Token (até limite)"
for ((i=1; i<=REQUESTS_PER_TOKEN; i++)); do
  http_code=$(curl -s -o /dev/null -w "%{http_code}" -H "API_KEY: $TOKEN" "$SERVER_URL" || echo "000")
  read count blocked_ttl <<< $(get_token_status "$TOKEN")
  status_symbol="❌"
  [[ "$http_code" == "200" ]] && status_symbol="✅"
  [[ "$http_code" == "429" ]] && status_symbol="⚠️"
  echo "Req #$i | Status: $http_code $status_symbol | token count: $count | blocked TTL: $blocked_ttl"
  sleep 0.1
done

echo "🔹 Teste IP + Token (token deve prevalecer)"
for ((i=1; i<=REQUESTS_PER_TOKEN; i++)); do
  for ip in "${IP_LIST[@]}"; do
    http_code=$(curl -s -o /dev/null -w "%{http_code}" -H "X-Forwarded-For: $ip" -H "API_KEY: $TOKEN" "$SERVER_URL" || echo "000")
    read count blocked_ttl <<< $(get_token_status "$TOKEN")
    status_symbol="❌"
    [[ "$http_code" == "200" ]] && status_symbol="✅"
    [[ "$http_code" == "429" ]] && status_symbol="⚠️"
    echo "[$ip] Req #$i | Status: $http_code $status_symbol | token count: $count | blocked TTL: $blocked_ttl"
    sleep 0.05
  done
done

echo "🔹 Teste desbloqueio automático (esperando 25 segundos)"
sleep 25
for ip in "${IP_LIST[@]}"; do
  http_code=$(curl -s -o /dev/null -w "%{http_code}" -H "X-Forwarded-For: $ip" "$SERVER_URL" || echo "000")
  read _ blocked_ttl <<< $(get_ip_status "$ip")
  echo "[$ip] Status pós-desbloqueio: $http_code | blocked TTL: $blocked_ttl"
done
http_code=$(curl -s -o /dev/null -w "%{http_code}" -H "API_KEY: $TOKEN" "$SERVER_URL" || echo "000")
read _ blocked_ttl <<< $(get_token_status "$TOKEN")
echo "[TOKEN] Status pós-desbloqueio: $http_code | blocked TTL: $blocked_ttl"

echo -e "\n----------------------------------------"
echo "🔎 Estado final no Redis:"
for ip in "${IP_LIST[@]}"; do
  read count blocked_ttl <<< $(get_ip_status "$ip")
  echo "IP: $ip | count: $count | blocked TTL: $blocked_ttl"
done
read count blocked_ttl <<< $(get_token_status "$TOKEN")
echo "TOKEN: $TOKEN | count: $count | blocked TTL: $blocked_ttl"

echo -e "\n🧹 Limpando Redis novamente..."
$REDIS_CLI FLUSHDB >/dev/null
echo "✅ Teste completo finalizado e Redis limpo!"
