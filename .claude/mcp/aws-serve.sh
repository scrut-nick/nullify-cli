#!/bin/bash
# Start the Agent Toolkit AWS MCP server (https://aws-mcp.us-east-1.api.aws/mcp)
# against dedicated read-only credentials.
#
# Why the credentials travel as SCRUT_AWS_* rather than AWS_*:
# cloud-session sandboxes already export AWS_ACCESS_KEY_ID and
# AWS_SECRET_ACCESS_KEY for their own object storage, and with-op-env.sh
# deliberately leaves already-set vars untouched. Credentials named AWS_* would
# therefore never be fetched from 1Password, and this server would sign its
# calls with the sandbox's identity - querying the wrong account and reporting
# the result as though it were ours. They are mapped onto AWS_* here instead,
# after the 1Password lookup has happened.
#
# --read-only drops every tool whose readOnlyHint is not true. Measured against
# the live endpoint, that leaves 6 of 9 tools: documentation search, skill
# retrieval and region metadata.
#
# READ THIS BEFORE REMOVING THE FLAG. The two tools that actually reach the AWS
# API - aws___call_aws and aws___run_script - are annotated readOnlyHint=false,
# destructiveHint=true, because they are generic escape hatches that *can*
# write whatever they are asked for; aws___run_script executes arbitrary Python
# with boto3. --read-only therefore excludes them, and with the flag on this
# server answers documentation questions only: it cannot describe a NACL, a
# route table or a WAF web ACL.
#
# Making it answer live infrastructure questions means dropping --read-only,
# at which point the IAM principal in SCRUT_AWS_* is the only thing preventing
# a mutation. Do that only once those credentials are a strictly read-only
# principal (AWS managed ReadOnlyAccess, or tighter).
#
# (The proxy's own --help contains a contradictory example captioned "Run with
# write permissions enabled" for this same flag. The flag description --
# "Disable tools which may require write permissions" -- is the accurate one;
# the tool counts above were measured, not inferred from the docs.)
set -uo pipefail

if [ -z "${SCRUT_AWS_ACCESS_KEY_ID:-}" ] || [ -z "${SCRUT_AWS_SECRET_ACCESS_KEY:-}" ]; then
  echo "aws-serve: SCRUT_AWS_ACCESS_KEY_ID / SCRUT_AWS_SECRET_ACCESS_KEY were not resolved." >&2
  echo "aws-serve: refusing to start rather than sign AWS calls with the sandbox's ambient credentials." >&2
  echo "aws-serve: add them to op://\${CLAUDE_OP_VAULT:-Claude}/\${CLAUDE_OP_ITEM:-cloud-session-env}." >&2
  exit 1
fi

export AWS_ACCESS_KEY_ID="$SCRUT_AWS_ACCESS_KEY_ID"
export AWS_SECRET_ACCESS_KEY="$SCRUT_AWS_SECRET_ACCESS_KEY"
if [ -n "${SCRUT_AWS_SESSION_TOKEN:-}" ]; then
  export AWS_SESSION_TOKEN="$SCRUT_AWS_SESSION_TOKEN"
else
  # A stale ambient session token would invalidate the key pair above.
  unset AWS_SESSION_TOKEN
fi
unset SCRUT_AWS_ACCESS_KEY_ID SCRUT_AWS_SECRET_ACCESS_KEY SCRUT_AWS_SESSION_TOKEN

# Default region only. The estate spans us-east-1, us-east-2, us-west-2,
# eu-north-1, ap-south-1 and ap-southeast-2, so region-specific questions must
# name their region in the call rather than relying on this.
export AWS_REGION="${SCRUT_AWS_REGION:-us-east-2}"

exec uvx mcp-proxy-for-aws@1.6.4 \
  https://aws-mcp.us-east-1.api.aws/mcp \
  --read-only \
  --region "$AWS_REGION" \
  --metadata "AWS_REGION=${AWS_REGION}"
