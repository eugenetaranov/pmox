# GitHub — repos, issues, pull requests, Actions (GitHub's official server; Docker).
# https://github.com/github/github-mcp-server
DESCRIPTION="GitHub: repositories, issues, pull requests and Actions"
KIND=docker
IMAGE=ghcr.io/github/github-mcp-server:v2.0.2
REQUIRED="GITHUB_PERSONAL_ACCESS_TOKEN"
skeleton() {
  cat <<'SKEL'
# GitHub — create a fine-grained token at https://github.com/settings/personal-access-tokens
# (pick the repositories, and only the permissions the agent needs).
GITHUB_PERSONAL_ACCESS_TOKEN=

# Optional: GitHub Enterprise Server or ghe.com
#GITHUB_HOST=

# Optional: limit the tools (comma-separated toolsets, e.g. repos,issues,pull_requests)
#GITHUB_TOOLSETS=

# Optional: read-only tools only
#GITHUB_READ_ONLY=1
SKEL
}
