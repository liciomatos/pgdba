#!/usr/bin/env bash
set -e

CONTAINER=postgres_pgdba

echo "==> Iniciando cenario de bloqueio de sessao..."

# Sessao A: abre transacao e segura lock por 2 minutos
podman exec -d "$CONTAINER" psql -U postgres -d mydb \
    -c "BEGIN; UPDATE test_locks SET val='locked' WHERE id=1; SELECT pg_sleep(120); COMMIT;"

sleep 0.5

# Sessao B: tenta atualizar a mesma linha → fica bloqueada
podman exec -d "$CONTAINER" psql -U postgres -d mydb \
    -c "UPDATE test_locks SET val='blocked' WHERE id=1;"

echo ""
echo "Cenario criado: uma sessao esta bloqueada tentando atualizar test_locks."
echo "Abra pgdba e selecione 'Blocked Queries' para visualizar."
echo ""
echo "Para limpar: make scenarios-clean"
