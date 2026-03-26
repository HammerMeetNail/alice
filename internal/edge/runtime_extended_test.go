package edge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadConfig_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(path, []byte("{not valid json"), 0o600); err != nil {
		t.Fatalf("write bad config: %v", err)
	}

	_, err := LoadConfig(path)
	if err == nil {
		t.Fatal("expected error loading invalid JSON config")
	}
	if !strings.Contains(err.Error(), "decode config") {
		t.Fatalf("expected 'decode config' in error, got %q", err.Error())
	}
}

func TestLoadConfig_MissingRequired(t *testing.T) {
	dir := t.TempDir()

	cases := []struct {
		name    string
		content string
		want    string
	}{
		{
			name:    "missing org_slug",
			content: `{"agent":{"owner_email":"a@b.com","agent_name":"x"},"server":{"base_url":"http://localhost"},"runtime":{"state_file":"s.json"}}`,
			want:    "org_slug is required",
		},
		{
			name:    "missing owner_email",
			content: `{"agent":{"org_slug":"org","agent_name":"x"},"server":{"base_url":"http://localhost"},"runtime":{"state_file":"s.json"}}`,
			want:    "owner_email is required",
		},
		{
			name:    "missing agent_name",
			content: `{"agent":{"org_slug":"org","owner_email":"a@b.com"},"server":{"base_url":"http://localhost"},"runtime":{"state_file":"s.json"}}`,
			want:    "agent_name is required",
		},
		{
			name:    "missing base_url",
			content: `{"agent":{"org_slug":"org","owner_email":"a@b.com","agent_name":"x"},"server":{},"runtime":{"state_file":"s.json"}}`,
			want:    "base_url is required",
		},
		{
			name:    "missing state_file",
			content: `{"agent":{"org_slug":"org","owner_email":"a@b.com","agent_name":"x"},"server":{"base_url":"http://localhost"},"runtime":{}}`,
			want:    "state_file is required",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(dir, tc.name+".json")
			if err := os.WriteFile(path, []byte(tc.content), 0o600); err != nil {
				t.Fatalf("write config: %v", err)
			}
			_, err := LoadConfig(path)
			if err == nil {
				t.Fatal("expected validation error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected %q in error, got %q", tc.want, err.Error())
			}
		})
	}
}

func TestConnectorReauthError_Message(t *testing.T) {
	cases := []struct {
		err  *ConnectorReauthRequiredError
		want string
	}{
		{
			err:  &ConnectorReauthRequiredError{ConnectorType: "github"},
			want: "github connector requires re-authorization",
		},
		{
			err:  &ConnectorReauthRequiredError{ConnectorType: "jira", Reason: "token expired"},
			want: "jira connector requires re-authorization: token expired",
		},
		{
			err:  (*ConnectorReauthRequiredError)(nil),
			want: "",
		},
	}
	for _, tc := range cases {
		got := tc.err.Error()
		if got != tc.want {
			t.Errorf("ConnectorReauthRequiredError.Error() = %q, want %q", got, tc.want)
		}
	}
}

func TestCredentialStoreKeyRequiredError_Message(t *testing.T) {
	cases := []struct {
		err  *CredentialStoreKeyRequiredError
		want string
	}{
		{
			err:  &CredentialStoreKeyRequiredError{Path: "/creds.json"},
			want: `credential store "/creds.json" requires a credentials key`,
		},
		{
			err:  &CredentialStoreKeyRequiredError{Path: "/creds.json", Reason: "file is encrypted"},
			want: `credential store "/creds.json" requires a credentials key: file is encrypted`,
		},
		{
			err:  (*CredentialStoreKeyRequiredError)(nil),
			want: "",
		},
	}
	for _, tc := range cases {
		got := tc.err.Error()
		if got != tc.want {
			t.Errorf("CredentialStoreKeyRequiredError.Error() = %q, want %q", got, tc.want)
		}
	}
}

func TestCredentialStoreDecryptError_Message(t *testing.T) {
	cases := []struct {
		err  *CredentialStoreDecryptError
		want string
	}{
		{
			err:  &CredentialStoreDecryptError{Path: "/creds.json"},
			want: `credential store "/creds.json" could not be decrypted`,
		},
		{
			err:  &CredentialStoreDecryptError{Path: "/creds.json", Reason: "wrong key"},
			want: `credential store "/creds.json" could not be decrypted: wrong key`,
		},
		{
			err:  (*CredentialStoreDecryptError)(nil),
			want: "",
		},
	}
	for _, tc := range cases {
		got := tc.err.Error()
		if got != tc.want {
			t.Errorf("CredentialStoreDecryptError.Error() = %q, want %q", got, tc.want)
		}
	}
}
