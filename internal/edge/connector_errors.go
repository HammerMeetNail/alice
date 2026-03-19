package edge

import "fmt"

type ConnectorReauthRequiredError struct {
	ConnectorType string
	Reason        string
}

func (e *ConnectorReauthRequiredError) Error() string {
	if e == nil {
		return ""
	}
	if e.Reason == "" {
		return fmt.Sprintf("%s connector requires re-authorization", e.ConnectorType)
	}
	return fmt.Sprintf("%s connector requires re-authorization: %s", e.ConnectorType, e.Reason)
}

type CredentialStoreKeyRequiredError struct {
	Path   string
	Reason string
}

func (e *CredentialStoreKeyRequiredError) Error() string {
	if e == nil {
		return ""
	}
	if e.Reason == "" {
		return fmt.Sprintf("credential store %q requires a credentials key", e.Path)
	}
	return fmt.Sprintf("credential store %q requires a credentials key: %s", e.Path, e.Reason)
}

type CredentialStoreDecryptError struct {
	Path   string
	Reason string
}

func (e *CredentialStoreDecryptError) Error() string {
	if e == nil {
		return ""
	}
	if e.Reason == "" {
		return fmt.Sprintf("credential store %q could not be decrypted", e.Path)
	}
	return fmt.Sprintf("credential store %q could not be decrypted: %s", e.Path, e.Reason)
}
