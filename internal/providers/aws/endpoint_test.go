package aws

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/aws/aws-sdk-go-v2/service/sts/types"
)

func newValidCredsSTSClient() STSAssumeRoler {
	return &mockSTSClient{
		assumeRoleFn: func(ctx context.Context, params *sts.AssumeRoleInput, optFns ...func(*sts.Options)) (*sts.AssumeRoleOutput, error) {
			return &sts.AssumeRoleOutput{
				Credentials: &types.Credentials{
					AccessKeyId:     awssdk.String("AKIAIOSFODNN7EXAMPLE"),
					SecretAccessKey: awssdk.String("wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"),
					SessionToken:    awssdk.String("FwoGZXIvYXdzEBY..."),
					Expiration:      awssdk.Time(time.Now().Add(time.Hour)),
				},
			}, nil
		},
	}
}

func TestEndpointClaudeFormat(t *testing.T) {
	h := &EndpointHandler{cfg: &Config{RoleARN: "arn:aws:iam::1:role/r", Region: "us-west-2", SessionDuration: time.Hour}}
	h.SetSTSClient(newValidCredsSTSClient())

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/_aws/credentials?format=claude", nil)
	h.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var got struct {
		Credentials struct {
			AccessKeyId, SecretAccessKey, SessionToken string
		}
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v body=%s", err, rec.Body.String())
	}
	if got.Credentials.AccessKeyId == "" || got.Credentials.SecretAccessKey == "" || got.Credentials.SessionToken == "" {
		t.Errorf("claude envelope missing creds: %s", rec.Body.String())
	}
}

func TestEndpointDefaultFormatUnchanged(t *testing.T) {
	h := &EndpointHandler{cfg: &Config{RoleARN: "arn:aws:iam::1:role/r", Region: "us-west-2", SessionDuration: time.Hour}}
	h.SetSTSClient(newValidCredsSTSClient())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/_aws/credentials", nil))
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["Version"] == nil || got["AccessKeyId"] == nil {
		t.Errorf("default credential_process shape changed: %s", rec.Body.String())
	}
}
