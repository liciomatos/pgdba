#!/usr/bin/env bash

CONTAINER=postgres_pgdba

echo "==> Removendo cenarios dinamicos..."

podman exec "$CONTAINER" psql -U postgres -d mydb -c "
-- Terminar queries de pg_sleep e sessoes bloqueadas em test_locks
SELECT pg_terminate_backend(pid)
FROM pg_stat_activity
WHERE state != 'idle'
  AND (query LIKE '%pg_sleep%' OR query LIKE '%test_locks%')
  AND pid != pg_backend_pid();

-- Remover replication slot de teste se existir
DO \$\$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_replication_slots WHERE slot_name = 'test_slot') THEN
        PERFORM pg_drop_replication_slot('test_slot');
        RAISE NOTICE 'Slot test_slot removido.';
    END IF;
END
\$\$;
"

echo "Cenarios dinamicos removidos."
echo "Nota: dados de test_data e test_bloat permanecem (use 'make seed' para recriar)."
