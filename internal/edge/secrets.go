package edge

import (
	"fmt"
	"os"
	"strings"
	"time"
)

func loadConnectorSecret(label, envVar, filePath string, credential ConnectorCredential) (string, error) {
	if strings.TrimSpace(envVar) != "" {
		if value := strings.TrimSpace(os.Getenv(envVar)); value != "" {
			return value, nil
		}
	}

	if strings.TrimSpace(filePath) != "" {
		data, err := os.ReadFile(filePath)
		if err != nil {
			return "", fmt.Errorf("read %s token file: %w", label, err)
		}
		if value := strings.TrimSpace(string(data)); value != "" {
			return value, nil
		}
		return "", fmt.Errorf("%s token file %q is empty", label, filePath)
	}

	if value := strings.TrimSpace(credential.AccessToken); value != "" {
		if !credential.ExpiresAt.IsZero() && time.Now().UTC().After(credential.ExpiresAt) {
			return "", fmt.Errorf("%s connector token stored in edge state expired at %s", label, credential.ExpiresAt.UTC().Format(time.RFC3339))
		}
		return value, nil
	}

	switch {
	case strings.TrimSpace(envVar) != "":
		return "", fmt.Errorf("%s connector requires %s", label, envVar)
	default:
		return "", fmt.Errorf("%s connector requires a token source", label)
	}
}

func loadOptionalSecret(label, envVar, filePath string) (string, error) {
	if strings.TrimSpace(envVar) != "" {
		if value := strings.TrimSpace(os.Getenv(envVar)); value != "" {
			return value, nil
		}
	}

	if strings.TrimSpace(filePath) != "" {
		data, err := os.ReadFile(filePath)
		if err != nil {
			return "", fmt.Errorf("read %s secret file: %w", label, err)
		}
		return strings.TrimSpace(string(data)), nil
	}

	return "", nil
}
