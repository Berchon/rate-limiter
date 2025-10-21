#!/usr/bin/env bash
# test_rate_limiter_intercalated.sh
# Teste do rate limiter por IP com requisições intercaladas (round-robin)

set -euo pipefail

SERVER_URL="${SERVER_URL:-http://localhost:8080/protected}"
REDIS_CLI="${REDIS_CLI:-docker exec -i rate-limiter-redis redis-cli}"
IP_LIST=("1.1.1.1" "2.2.2.2" "3.3.3.3")
REQUESTS_PER_IP="${REQUESTS_PER_IP:-50}"
SLEEP_BETWEEN_REQUESTS="${SLEEP_BETWEEN_REQUESTS:-0.05}"

echo "🚀 Test Rate Limiter (intercalado)"
echo "Server: $SERVER_URL"
echo "IPs: ${IP_LIST[*]}"
echo "Requests per IP: $REQUESTS_PER_IP"
echo "----------------------------------------"

# Limpa o banco Redis antes do teste
echo "🧹 Limpando banco Redis (FLUSHDB)..."
$REDIS_CLI FLUSHDB
echo "Banco limpo com sucesso!"
echo "----------------------------------------"

# Para REQUESTS_PER_IP rounds, em cada round faz uma requisição para cada IP (round-robin)
for ((round=1; round<=REQUESTS_PER_IP; round++)); do
  echo -e "\n🔁 Round #$round"
  for ip in "${IP_LIST[@]}"; do
    http_code=$(curl -s -o /dev/null -w "%{http_code}" -H "X-Forwarded-For: $ip" "$SERVER_URL" || echo "000")

    if [[ "$http_code" == "200" ]]; then
      echo -n "✅ [$ip] round #$round -> 200 OK  "
    elif [[ "$http_code" == "429" ]]; then
      echo -n "⚠️  [$ip] round #$round -> 429 TOO MANY REQUESTS  "
    else
      echo -n "❌ [$ip] round #$round -> HTTP $http_code  "
    fi

    cnt=$($REDIS_CLI GET "ip:${ip}" 2>/dev/null || true)
    ttl=$($REDIS_CLI TTL "ip:${ip}" 2>/dev/null || echo "-2")
    blk_ttl=$($REDIS_CLI TTL "ip:${ip}:blocked" 2>/dev/null || echo "-2")
    echo " | count=${cnt:-0} ttl=${ttl} blocked_ttl=${blk_ttl}"

    sleep "$SLEEP_BETWEEN_REQUESTS"
  done
done

echo -e "\n----------------------------------------"
echo "🔎 Resultado final (contadores e bloqueios no Redis):"
for ip in "${IP_LIST[@]}"; do
  cnt=$($REDIS_CLI GET "ip:${ip}" 2>/dev/null || true)
  blk=$($REDIS_CLI GET "ip:${ip}:blocked" 2>/dev/null || true)
  blk_ttl=$($REDIS_CLI TTL "ip:${ip}:blocked" 2>/dev/null || echo "-2")
  ttl=$($REDIS_CLI TTL "ip:${ip}" 2>/dev/null || echo "-2")
  echo "IP: $ip | count=${cnt:-0} | ttl=${ttl} | blocked=${blk:-no} | blocked_ttl=${blk_ttl}"
done

echo -e "\n----------------------------------------"
echo "🧹 Limpando Redis novamente (FLUSHDB)..."
$REDIS_CLI FLUSHDB
echo "✅ Teste concluído e Redis limpo!"
