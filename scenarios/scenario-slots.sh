#!/usr/bin/env bash
set -e

CONTAINER=pgdba_main

echo "==> Criando replication slot de teste..."

exists=$(podman exec "$CONTAINER" psql -U postgres -d mydb -tAc \
    "SELECT 1 FROM pg_replication_slots WHERE slot_name = 'test_slot'")

if [ "$exists" = "1" ]; then
    echo "  slot test_slot ja existe."
else
    podman exec "$CONTAINER" psql -U postgres -d mydb -c \
        "SELECT pg_create_logical_replication_slot('test_slot', 'pgoutput')"
    echo "  slot test_slot criado."
fi

echo ""
echo "Cenario criado: replication slot 'test_slot' disponivel."
echo "Abra pgdba e selecione 'Replication Slots' para visualizar."
echo "(O slot aparecera como inativo/false - use 'd' para dropar)"
echo ""
echo "Para limpar: make scenarios-clean"
