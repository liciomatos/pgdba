#!/usr/bin/env bash
set -e

CONTAINER=postgres_pgdba

echo "==> Criando replication slot de teste..."

podman exec "$CONTAINER" psql -U postgres -d mydb -c "
DO \$\$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_replication_slots WHERE slot_name = 'test_slot'
    ) THEN
        PERFORM pg_create_logical_replication_slot('test_slot', 'pgoutput');
        RAISE NOTICE 'Slot test_slot criado.';
    ELSE
        RAISE NOTICE 'Slot test_slot ja existe.';
    END IF;
END
\$\$;
"

echo ""
echo "Cenario criado: replication slot 'test_slot' disponivel."
echo "Abra pgdba e selecione 'Replication Slots' para visualizar."
echo "(O slot aparecera como inativo/false - use 'd' para dropar)"
echo ""
echo "Para limpar: make scenarios-clean"
