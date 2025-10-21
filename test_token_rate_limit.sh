#!/usr/bin/env bash
# test_token_rate_limit.sh
# Teste completo do rate limiter por Token

set -euo pipefail

SERVER_URL="${SERVER_URL:-http://localhost:8080/protected}"
API_KEY_URL="${API_KEY_URL:-http://localhost:8080/api-key}"
REDIS_CLI="${REDIS_CLI:-docker exec -i rate-limiter-redis redis-cli}"
REQUESTS="${REQUESTS:-12}"  # Número de requests a testar

echo "🚀 Teste Rate Limiter por Token (full debug)"
echo "Server: $SERVER_URL"
echo "Requests: $REQUESTS"
echo "----------------------------------------"

# 1️⃣ Limpar Redis antes do teste
echo "🧹 Limpando Redis..."
$REDIS_CLI FLUSHDB >/dev/null
echo "Redis limpo!"
echo "----------------------------------------"

# 2️⃣ Criar token dinamicamente
TOKEN_JSON=$(curl -s -X POST "$API_KEY_URL" \
  -H 'Content-Type: application/json' \
  -d '{"time_window": 120, "max_requests": 10, "blocked_duration": 20}')
echo "Token JSON retornado: $TOKEN_JSON"

# Extrai token usando sed (compatível em qualquer shell)
TOKEN=$(echo "$TOKEN_JSON" | sed -n 's/.*"token":"\([^"]*\)".*/\1/p')
echo "Token criado: $TOKEN"

# 3️⃣ Executar requisições
for ((i=1; i<=REQUESTS; i++)); do
  echo -e "\n🔹 Requisição $i"
  HTTP_STATUS=$(curl -s -o /dev/null -w "%{http_code}" -H "API_KEY: $TOKEN" "$SERVER_URL")
  echo "HTTP Status: $HTTP_STATUS"

  # Mostrar TTL do token no Redis
  TTL=$($REDIS_CLI TTL "$TOKEN" 2>/dev/null || echo "-2")
  echo "Token TTL: $TTL segundos"

  # Checar se bloqueio está ativo
  BLOCKED_TTL=$($REDIS_CLI TTL "${TOKEN}:blocked" 2>/dev/null || echo "-2")
  if [[ "$BLOCKED_TTL" -gt 0 ]]; then
    echo "Bloqueio ativo! Tempo restante: $BLOCKED_TTL segundos"
  else
    echo "Bloqueio não ativo"
  fi

  sleep 0.5  # Pequeno delay entre requests
done

echo -e "\n✅ Teste concluído"
