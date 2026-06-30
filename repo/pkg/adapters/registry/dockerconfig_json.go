package registry

import (
	"encoding/base64"
	"encoding/json"
	"strings"
)

func BuildDockerConfigJSON(registryHost, username, password string) (string, error) {
	host := strings.TrimSpace(registryHost)
	if host == "" || strings.TrimSpace(username) == "" || password == "" {
		return "", nil
	}
	auth := base64.StdEncoding.EncodeToString([]byte(username + ":" + password))
	payload, err := json.Marshal(map[string]any{
		"auths": map[string]any{
			host: map[string]string{
				"username": username,
				"password": password,
				"auth":     auth,
			},
		},
	})
	if err != nil {
		return "", err
	}
	return string(payload), nil
}
