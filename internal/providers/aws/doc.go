// Package aws implements the AWS credential provider for moat.
//
// Unlike other providers that inject credentials via proxy headers, AWS uses
// a credential endpoint pattern. The provider exposes an HTTP endpoint that
// returns temporary credentials in ECS container format.
//
// The container is configured with AWS_CONTAINER_CREDENTIALS_FULL_URI pointing
// to the proxy's credential endpoint, allowing AWS SDKs to automatically fetch
// credentials when needed.
//
// Credentials are acquired in one of two modes selected by the `source` metadata
// key: `role` (default — moat calls `sts:AssumeRole` on the stored role ARN) or
// `profile` (moat serves the named AWS shared-config profile's resolved credentials
// directly, without AssumeRole; useful when the profile's `credential_process`
// calls a broker that issues credentials directly).
//
// Grant flow (role mode):
//  1. User provides IAM role ARN via `moat grant aws --role`
//  2. ARN is validated and tested with STS AssumeRole
//  3. Role ARN stored in Credential.Token, region/duration in Metadata
//
// Grant flow (profile mode):
//  1. User provides profile name via `moat grant aws --aws-profile`
//  2. Profile is validated by loading it and calling Credentials.Retrieve
//  3. Profile name stored in Credential.Metadata["profile"]; Token is empty
//
// Runtime flow:
//  1. Container makes AWS API call
//  2. AWS SDK detects AWS_CONTAINER_CREDENTIALS_FULL_URI
//  3. SDK fetches credentials from proxy endpoint
//  4. Proxy returns credentials (via AssumeRole in role mode, or directly from
//     the profile's credential provider chain in profile mode)
//  5. SDK uses credentials for the API call
package aws
