# AWS API — run AWS CLI operations (AWS Labs; Docker).
# https://github.com/awslabs/mcp/tree/main/src/aws-api-mcp-server
DESCRIPTION="AWS: run AWS CLI commands against your account"
KIND=docker
IMAGE=public.ecr.aws/awslabs-mcp/awslabs/aws-api-mcp-server:1.5.6
REQUIRED="AWS_REGION"
# Files in ~/.mcp/aws-api/ are mounted at /mcp, so an AWS config/credentials
# pair dropped there is picked up via AWS_PROFILE.
DOCKER_ARGS="-e AWS_CONFIG_FILE=/mcp/config -e AWS_SHARED_CREDENTIALS_FILE=/mcp/credentials"
skeleton() {
  cat <<'SKEL'
# AWS — use a least-privilege (ideally read-only) IAM user.
AWS_REGION=

# Either static keys here ...
#AWS_ACCESS_KEY_ID=
#AWS_SECRET_ACCESS_KEY=
# ... or put `config` and `credentials` files next to this env file and pick
# a profile:
#AWS_PROFILE=

# Only allow read operations (recommended)
READ_OPERATIONS_ONLY=true
SKEL
}
