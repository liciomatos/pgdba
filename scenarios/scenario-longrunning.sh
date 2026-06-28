#!/usr/bin/env bash
set -e

CONTAINER=postgres_pgdba

echo "==> Iniciando query de longa duracao (300s)..."

podman exec -d "$CONTAINER" psql -U postgres -d mydb \
    -c "SELECT pg_sleep(300), 'pgdba-cli long running test' AS description;"

echo ""
echo "Cenario criado: query dormindo por 300 segundos."
echo "Abra pgdba e selecione 'Long Running Queries' para visualizar."
echo ""
echo "Para limpar: make scenarios-clean"
