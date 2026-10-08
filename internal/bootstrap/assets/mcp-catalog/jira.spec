# Jira (and optionally Confluence) via mcp-atlassian (Docker).
# https://github.com/sooperset/mcp-atlassian
DESCRIPTION="Jira and Confluence: search, read and update issues and pages"
KIND=docker
IMAGE=ghcr.io/sooperset/mcp-atlassian:0.23.1
REQUIRED="JIRA_URL JIRA_USERNAME JIRA_API_TOKEN"
skeleton() {
  cat <<'SKEL'
# Jira — create an API token at https://id.atlassian.com/manage-profile/security/api-tokens
JIRA_URL=
JIRA_USERNAME=
JIRA_API_TOKEN=

# Optional: Confluence through the same server
#CONFLUENCE_URL=
#CONFLUENCE_USERNAME=
#CONFLUENCE_API_TOKEN=

# Optional: expose read-only tools only
#READ_ONLY_MODE=true
SKEL
}
