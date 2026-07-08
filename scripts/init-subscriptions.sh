#!/bin/bash
set -e

# Subscriptions are created on pg-sub and subscribe to publications on pg-main.
# Cross-server logical replication does not suffer from the self-subscription
# deadlock, so no pre-created slot workaround is needed here.
SUB_CONTAINER=pgdba_sub
DB=mydb
CONN="host=pg-main port=5432 user=postgres password=postgres dbname=$DB"

create_sub() {
    local name=$1
    local pub=$2

    local exists
    exists=$(podman exec "$SUB_CONTAINER" psql -U postgres -d "$DB" -tAc \
        "SELECT 1 FROM pg_subscription WHERE subname = '$name'")
    if [ "$exists" = "1" ]; then
        echo "  subscription $name already exists, skipping."
        return
    fi

    podman exec "$SUB_CONTAINER" psql -U postgres -d "$DB" -c \
        "CREATE SUBSCRIPTION $name
             CONNECTION '$CONN'
             PUBLICATION $pub"
    echo "  subscription $name created."
}

create_sub orders_sub    orders_pub
create_sub inventory_sub inventory_pub
