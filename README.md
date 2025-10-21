# 🚦 Go Rate Limiter — Limitação de Requisições por IP e Token (Go + Redis)

[![Go](https://img.shields.io/badge/Go-1.24-blue)](https://golang.org/)  
[![Docker](https://img.shields.io/badge/Docker-Enabled-blue)](https://www.docker.com/)  
[![Redis](https://img.shields.io/badge/Redis-Enabled-%23D82C2C)](https://redis.io/)  

Sistema de Rate Limiting desenvolvido em Go, que permite limitar requisições por endereço IP ou token de acesso.
A aplicação utiliza Redis como mecanismo de armazenamento e conta com testes automatizados e docker-compose para fácil execução.

## 📌 Objetivo do Projeto

Criar um Rate Limiter configurável que:
  - Limite o número de requisições por IP dentro de uma janela de tempo definida.
  - Limite o número de requisições por token de acesso, com prioridade sobre o IP.
  - Bloqueie temporariamente IPs ou tokens que ultrapassarem o limite.
  - Permita configuração via variáveis de ambiente.
  - Seja implementado como middleware reutilizável para servidores HTTP.

## 🧾 Requisitos do Projeto

Desenvolver um rate limiter em Go que possa ser configurado para limitar o número máximo de requisições por segundo com base em um endereço IP específico ou em um token de acesso.

**Descrição**: O objetivo deste desafio é criar um rate limiter em Go que possa ser utilizado para controlar o tráfego de requisições para um serviço web. O rate limiter deve ser capaz de limitar o número de requisições com base em dois critérios:

1. **Endereço IP**: O rate limiter deve restringir o número de requisições recebidas de um único endereço IP dentro de um intervalo de tempo definido.
2. **Token de Acesso**: O rate limiter deve também poderá limitar as requisições baseadas em um token de acesso único, permitindo diferentes limites de tempo de expiração para diferentes tokens. O Token deve ser informado no header no seguinte formato:
    - API_KEY: <TOKEN>  
3. As configurações de limite do token de acesso devem se sobrepor as do IP. Ex: Se o limite por IP é de 10 req/s e a de um determinado token é de 100 req/s, o rate limiter deve utilizar as informações do token.

**Requisitos**:

- O rate limiter deve poder trabalhar como um middleware que é injetado ao servidor web
- O rate limiter deve permitir a configuração do número máximo de requisições permitidas por segundo.
- O rate limiter deve ter ter a opção de escolher o tempo de bloqueio do IP ou do Token caso a quantidade de requisições tenha sido excedida.
- As configurações de limite devem ser realizadas via variáveis de ambiente ou em um arquivo “.env” na pasta raiz.
- Deve ser possível configurar o rate limiter tanto para limitação por IP quanto por token de acesso.
- O sistema deve responder adequadamente quando o limite é excedido:
  - Código HTTP: **429**
  - Mensagem: **you have reached the maximum number of requests or actions allowed within a certain time frame**
- odas as informações de "limiter” devem ser armazenadas e consultadas de um banco de dados Redis. Você pode utilizar docker-compose para subir o Redis.
- Crie uma “strategy” que permita trocar facilmente o Redis por outro mecanismo de persistência.
- A lógica do limiter deve estar separada do middleware.

## 📊 Cenários de Exemplo

### Limitação por IP:

* Configuração: máximo de 5 requisições por 5 segundos.
* Se o IP `192.168.1.1` fizer 6 requisições no período, a sexta será bloqueada com `429 Too Many Requests`.

### Limitação por Token:

* Token `abc123` configurado com 10 requisições a cada 5 segundos.
* Se fizer 11 requisições, a 11ª é bloqueada, e o token fica suspenso pelo tempo configurado.
* As regras do token **têm precedência sobre o IP**.

## 🧩 O que foi implementado (resumo)

* Middleware `RateLimitMiddleware` para controle de requisições.
* Estratégia de limitação por **IP** e por **Token**.
* Criação dinâmica de tokens via endpoint `/api-key`.
* Armazenamento e controle de bloqueios no `Redis`.
* Respostas padronizadas (HTTP 429 e mensagem JSON).
* Testes de integração e unitários cobrindo:
* Scripts bash para testes mistos.

## ⚙️ Variáveis de Ambiente

O arquivo .env fica na raiz do projeto.
Antes de rodar o projeto, copie o .env.example:
```bash
cp .env.example .env
```

| Variável | Exemplo | Descrição |
|----------|----|----------------|
| APP_PORT |	8080 |	Porta da aplicação. |
| REDIS_HOST |	localhost |	Host do Redis. |
| REDIS_PORT |	6379 |	Porta do Redis. |
| REDIS_DB |	0 |	Número do banco Redis. |
| REDIS_PASSWORD |	(vazio) |	Senha opcional do Redis. |
| RATE_BY_IP_TIME_WINDOW |	5 |	Janela de tempo (segundos) para contagem de requisições por IP. |
| RATE_BY_IP_MAX_REQUESTS |	2 |	Máximo de requisições permitidas por IP na janela. |
| RATE_BY_IP_BLOCKED_DURATION |	60 |	Duração (segundos) do bloqueio do IP após atingir o limite. |

## 🧱 Como Rodar o Projeto

### 💾 Passo 1 - Clonar repositório
```bash
git clone https://github.com/Berchon/rate-limiter.git
cd rate-limiter
```

### ⚙️ Passo 2 - Criar o arquivo .env
```bash
cp .env.example .env
```

### 🪄 Passo 3 — Subir containers do Redis
```bash
docker compose up -d
```

> Isso sobe o Redis em localhost:6379 com volume persistente.

### 🚀 Passo 4 — Rodar a aplicação
```bash
go run ./cmd/webapp/main.go
```
> O servidor estará disponível em http://localhost:8080.

## 🌐 Endpoints da Aplicação

As requisições estão organizadas em ./API/ (compatíveis com o plugin REST Client do VSCode ou curl).

### 🔍 Health Check
```bash
GET http://localhost:8080/health
```

### 🏓 Ping
```bash
GET http://localhost:8080/ping
```

### 🔒 Endpoint protegido (sem token)
```bash
GET http://localhost:8080/protected
```

### 🔑 Endpoint protegido (com token)
```bash
GET http://localhost:8080/protected
HEADER API_KEY: 5e85a15417f8f2cc07a299242d79aac0
```

### 🧩 Criar um novo token (API Key)
```bash
POST http://localhost:8080/api-key
Content-Type: application/json

{
  "time_window": 5,
  "max_requests": 2,
  "blocked_duration": 5,
  "token_ttl": 3600
}
```

### 🧾 Exemplo de resposta

#### 201
```json
{
  "token": "22907993820d6c7faf682e2f16c8dffa",
  "time_window": 5,
  "max_requests": 2,
  "blocked_duration": 5,
  "created_at": "2025-10-21T08:53:54-03:00",
  "expires_at": "2025-10-21T09:53:54-03:00"
}
```

### ❌ Exemplo de resposta quando bloqueado

#### 429
```json
{
  "error": "you have reached the maximum number of requests or actions allowed within a certain time frame"
}
```

### ✅ Exemplo de resposta quando bloqueado

#### 200
```json
protected content
```

## 🧪 Testes Automatizados

O projeto inclui testes unitários e de integração, validando comportamento de bloqueio e uso do Redis

### 🧩 Testes Unitários
```bash
go test ./internal/... -v
```

### 🔗 Testes de Integração
```bash
go test ./tests/integration/... -v
```

### 🧬 Scripts Bash de Teste
```bash
./test_ip_rate-limit.sh        # Teste por IP. Um IP por vez
./test_ip_rate-limit2.sh        # Teste por IP. IPs intercalados
./test_token_rate-limit.sh      # Teste por token
./test_mix_rate-limit.sh       # Teste combinado por IP e token
```

## 🧠 Comportamento do Rate Limiter

1. A cada requisição recebida, o middleware identifica:
    - Se há um token válido no header API_KEY.
    - Caso contrário, usa o IP como chave de limitação.
2. Os dados são armazenados no Redis com TTL e contadores.
3. Ao atingir o limite:
    - A chave (IP ou token) é bloqueada por blocked_duration.
    - O servidor retorna HTTP 429 e mensagem padrão.
4. Após o período, o bloqueio expira e o acesso é restaurado automaticamente.

## 🧰 Tecnologias e Bibliotecas Utilizadas

- Golang 1.24
- Redis 7 (Alpine)
- Docker / Docker Compose
- HTTP Middleware
- Go Testing / Testify

## 📂 Estrutura Completa do Projeto
```bash
rate-limiter/
├── API/
│   ├── health.http
│   ├── protected.http
│   └── api-key.http
│
├── internal/
│   ├── business/
│   │   ├── entity/
│   │   ├── dto/
│   │   ├── service/
│   │   └── usecase/
│   ├── infra/
│   │   ├── app/
│   │   ├── config/
│   │   ├── di/
│   │   ├── http/
│   │   │   ├── handler/
│   │   │   ├── middleware/
│   │   │   └── routes/
│   │   ├── redis/
│   │   └── storage/
│   └── tests/
│       └── integration/
│
├── docker-compose.yml
├── go.mod
├── .env
├── .env.example
├── test_ip_rate-limit.sh
├── test_mix_rate-limit.sh
└── README.md
```

## 👨‍💻 Autor

Projeto desenvolvido por Berchon — https://github.com/Berchon