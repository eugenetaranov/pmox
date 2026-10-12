# Postgres — schema, queries, explain plans and health checks (Postgres MCP Pro; Docker).
# https://github.com/crystaldba/postgres-mcp
DESCRIPTION="Postgres: schema, queries, explain plans and index advice"
KIND=docker
IMAGE=crystaldba/postgres-mcp:0.3.0
REQUIRED="DATABASE_URI"
# Host networking, so a database on this VM is reachable at 127.0.0.1.
DOCKER_ARGS="--network host"
skeleton() {
  cat <<'SKEL'
# Postgres — a connection URI, postgresql://user:password@host:5432/dbname
# (https://www.postgresql.org/docs/current/libpq-connect.html#LIBPQ-CONNSTRING-URIS).
# For a database on this VM use 127.0.0.1, not
# "localhost" (the image rewrites localhost to the Docker bridge).
# Use a read-only role where you can.
DATABASE_URI=
SKEL
}
