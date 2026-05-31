#!/bin/sh
set -e

psql -c "CREATE TABLE IF NOT EXISTS schema_migrations (
    filename    TEXT        PRIMARY KEY,
    applied_at  TIMESTAMPTZ DEFAULT NOW()
);"

for f in $(ls /migrations/*.sql | sort); do
    name=$(basename "$f")
    count=$(psql -tAc "SELECT COUNT(*) FROM schema_migrations WHERE filename = '$name'")
    if [ "$count" = "0" ]; then
        echo "==> Applying: $name"
        psql -f "$f"
        psql -c "INSERT INTO schema_migrations (filename) VALUES ('$name')"
    else
        echo "--> Already applied: $name"
    fi
done

echo "All migrations complete."
