# Linear — issues, projects and comments (Linear's hosted server; remote HTTP).
# https://linear.app/docs/mcp
DESCRIPTION="Linear: search, create and update issues, projects and comments"
KIND=http
URL=https://mcp.linear.app/mcp
AUTH=bearer
AUTH_TOKEN_VAR=LINEAR_API_KEY
REQUIRED="LINEAR_API_KEY"
skeleton() {
  cat <<'SKEL'
# Linear — create a personal API key at https://linear.app/settings/account/security
LINEAR_API_KEY=
SKEL
}
