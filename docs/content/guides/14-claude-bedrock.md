---
title: "Claude on AWS Bedrock"
navTitle: "Claude on Bedrock"
description: "Run Claude Code through AWS Bedrock using an IAM role and moat's AWS credential path."
keywords: ["moat", "claude code", "aws bedrock", "bedrock", "aws", "iam", "credential injection"]
---

# Claude on AWS Bedrock

This guide covers running Claude Code through AWS Bedrock instead of the Anthropic API. Moat handles credential delivery via Claude Code's `awsCredentialExport` hook — no long-lived credentials are written to the container.

## Prerequisites

- Moat installed
- An AWS IAM role with the following permissions:
  - `bedrock:InvokeModel`
  - `bedrock:InvokeModelWithResponseStream`
  - `bedrock:ListFoundationModels`
- Model access enabled in the Bedrock console for the models you want to use

Grant the role to moat:

```bash
moat grant aws arn:aws:iam::123456789012:role/my-bedrock-role
```

## Minimal working configuration

```yaml
agent: claude
grants:
  - aws

claude:
  bedrock:
    enabled: true
```

Run:

```bash
moat claude ./my-project
```

Moat sets `CLAUDE_CODE_USE_BEDROCK=1` and the Bedrock model ID vars, then injects short-lived STS credentials via Claude Code's `awsCredentialExport` hook.

## How credentials flow

1. Moat writes `awsCredentialExport: "/moat/aws/credentials --claude"` into the container's `~/.claude/settings.json`.
2. At session start (and every 5 minutes), Claude Code runs `/moat/aws/credentials --claude`.
3. The helper calls moat's AWS credential endpoint (`/_aws/credentials`), which performs server-side `AssumeRole` with caching and returns fresh STS credentials.
4. The helper reshapes the response into the `{"Credentials":{"AccessKeyId":...,"SecretAccessKey":...,"SessionToken":...}}` envelope Claude Code expects.
5. Claude Code signs Bedrock API requests using the bundled AWS SDK. The moat proxy observes traffic via the installed CA bundle but does not re-sign requests.

Moat writes no long-lived credentials to the container. The role ARN and STS tokens live in moat's encrypted credential store on the host.

When `bedrock.enabled` is true, moat overrides any `awsCredentialExport` value from the host `~/.claude/settings.json` and removes `awsAuthRefresh` (a host-side SSO command that would fail inside the container).

## Model overrides

Moat ships built-in Bedrock model IDs for each model slot. Override any of them via `claude.bedrock.models`:

```yaml
claude:
  bedrock:
    enabled: true
    models:
      sonnet: global.anthropic.claude-sonnet-4-6[1m]
      opus: global.anthropic.claude-opus-4-6-v1[1m]
      haiku: global.anthropic.claude-haiku-4-5-20251001-v1:0
      custom: global.anthropic.claude-opus-4-7[1m]
```

Model ID strings (including the `[1m]` throughput modifier) pass through verbatim. Omit any slot to use the built-in default. The `models` fields map to the env vars `ANTHROPIC_DEFAULT_{HAIKU,SONNET,OPUS}_MODEL` and `ANTHROPIC_CUSTOM_MODEL_OPTION`.

## Region resolution

The effective region is selected in this order (highest wins):

1. `claude.bedrock.region` in `moat.yaml`
2. `AWS_REGION` in the merged settings env (see `claude.env` below)
3. Region stored with the `aws` grant
4. `us-east-1`

Specify a region explicitly when the model or your account requires it:

```yaml
claude:
  bedrock:
    enabled: true
    region: us-west-2
```

When `network.policy: strict`, moat automatically adds `bedrock-runtime.<region>.amazonaws.com` and `bedrock.<region>.amazonaws.com` to the allowed host list.

## Using a named AWS profile

If the host `~/.claude/settings.json` `env` block sets `AWS_PROFILE`, moat honors it. The `/moat/aws/config` file inside the container is written with both a `[default]` stanza and a `[profile <name>]` stanza pointing at moat's credential endpoint, so both the default SDK lookup and the named profile resolve correctly.

```json
{
  "env": {
    "AWS_PROFILE": "my-corp-profile"
  }
}
```

No `moat.yaml` change is needed — moat reads the `env` block from the host settings file automatically.

## Disabling telemetry and auto-updates

`claude.env` merges arbitrary variables into Claude Code's `settings.json` `env` block. To disable telemetry, the autoupdater, and non-essential network traffic:

```yaml
claude:
  bedrock:
    enabled: true
  env:
    CLAUDE_CODE_DISABLE_FEEDBACK_SURVEY: "1"
    CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC: "1"
    CLAUDE_CODE_ENABLE_TELEMETRY: "0"
    DISABLE_AUTOUPDATER: "1"
    DISABLE_BUG_COMMAND: "1"
    DISABLE_ERROR_REPORTING: "1"
    DISABLE_EXTRA_USAGE_COMMAND: "1"
    DISABLE_FEEDBACK_COMMAND: "1"
    DISABLE_INSTALLATION_CHECKS: "1"
    DISABLE_INSTALL_GITHUB_APP_COMMAND: "1"
    DISABLE_LOGIN_COMMAND: "1"
    DISABLE_LOGOUT_COMMAND: "1"
    DISABLE_TELEMETRY: "1"
    DISABLE_UPGRADE_COMMAND: "1"
```

`claude.env` applies even when Bedrock is not in use.

## Testing

Live Bedrock end-to-end testing requires a real IAM role and model access in AWS — moat's automated test suite does not cover live Bedrock calls. To verify the setup:

1. Grant the role: `moat grant aws <role-arn>`
2. Run a minimal prompt: `moat claude -p "say hello" ./my-project`
3. Check logs for Bedrock API calls: `moat trace --network`

If credentials are not refreshing, check that the IAM role trust policy allows `sts:AssumeRole` from the principal running moat.

## Troubleshooting

### "Bedrock mode needs grants: [aws]"

Add `aws` to `grants:` in `moat.yaml` and run `moat grant aws <role-arn>`.

### "claude.bedrock is mutually exclusive with claude.base_url / claude.llm-gateway"

Remove `claude.base_url` or `claude.llm-gateway` from `moat.yaml`. Both fields set `ANTHROPIC_BASE_URL`, which conflicts with Bedrock authentication.

### AccessDeniedException from Bedrock

Verify the IAM role has `bedrock:InvokeModel`, `bedrock:InvokeModelWithResponseStream`, and `bedrock:ListFoundationModels`. Also confirm model access is enabled in the Bedrock console for the target region.

### Wrong region

Set `claude.bedrock.region` explicitly to override the grant region default.

## Related guides

- [Running Claude Code](./01-claude-code.md) — Claude Code agent guide
- [AWS grant](../reference/04-grants.md#aws) — Storing and managing AWS credentials
- [moat.yaml reference](../reference/02-moat-yaml.md#claudebedrock) — Full field reference for `claude.bedrock` and `claude.env`
- [Network policy](../reference/02-moat-yaml.md#network) — Configuring strict outbound rules
