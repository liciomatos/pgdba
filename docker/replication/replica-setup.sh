#!/bin/bash
set -e

PRIMARY_HOST="postgres-primary"
PRIMARY_PORT="5432"
PRIMARY_USER="postgres"
PGDATA="/var/lib/postgresql/data"

echo "Waiting for primary to be ready..."
until pg_isready -h "$PRIMARY_HOST" -p "$PRIMARY_PORT" -U "$PRIMARY_USER"; do
  sleep 1
done

if [ -z "$(ls -A "$PGDATA")" ]; then
  echo "Cloning primary with pg_basebackup..."
  pg_basebackup \
    -h "$PRIMARY_HOST" \
    -p "$PRIMARY_PORT" \
    -U "$PRIMARY_USER" \
    -D "$PGDATA" \
    --wal-method=stream \
    --checkpoint=fast \
    -R \
    --no-password

  # Write primary_conninfo (pg_basebackup -R already writes postgresql.auto.conf)
  echo "primary_conninfo = 'host=$PRIMARY_HOST port=$PRIMARY_PORT user=$PRIMARY_USER application_name=pgdba_replica'" \
    >> "$PGDATA/postgresql.auto.conf"

  echo "hot_standby = on"            >> "$PGDATA/postgresql.auto.conf"
  echo "wal_level = replica"         >> "$PGDATA/postgresql.auto.conf"

  echo "pg_basebackup complete."
else
  echo "Data directory already exists; skipping pg_basebackup."
fi

exec docker-entrypoint.sh postgres \
  -c hot_standby=on \
  -c wal_level=replica
