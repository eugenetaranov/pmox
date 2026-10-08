# Jenkins — via the Jenkins "MCP Server" plugin, which serves MCP over HTTP
# from Jenkins itself (install the plugin on your controller first).
# https://plugins.jenkins.io/mcp-server/
DESCRIPTION="Jenkins: jobs, builds and logs"
KIND=http
URL_VAR=JENKINS_URL
URL_SUFFIX=/mcp-server/mcp
AUTH=basic
AUTH_USER_VAR=JENKINS_USER
AUTH_TOKEN_VAR=JENKINS_API_TOKEN
REQUIRED="JENKINS_URL JENKINS_USER JENKINS_API_TOKEN"
skeleton() {
  cat <<'SKEL'
# Jenkins — API token: <your Jenkins>/me/configure → API Token
JENKINS_URL=
JENKINS_USER=
JENKINS_API_TOKEN=
SKEL
}
