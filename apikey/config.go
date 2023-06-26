package apikey

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
)

const APIKeyEnvVarName string = "API_KEY_CONFIG"

type APIKeyConfig struct {
	Principal string   `json:"principal"`
	Keys      []string `json:"keys"`
}

type ConfigErrors struct {
	errs []error
}

func (ce ConfigErrors) GetErrors() []error {
	return ce.errs
}

func (ce ConfigErrors) LogErrors() {
	for _, e := range ce.errs {
		log.Println(e)
	}
}

func (ce ConfigErrors) Error() string {
	errStrings := []string{}
	for _, e := range ce.errs {
		errStrings = append(errStrings, e.Error())
	}
	return strings.Join(errStrings, ", ")
}

// GetConfigFromEnvJSON returns []*APIKeyConfig and ConfigErrors. In addition to satisfying the
// error interface, ConfigErrors has helper methods which provides the logging individual errors
// or returning the slice of errors for hands-on processing.
func GetConfigFromEnvJSON() ([]*APIKeyConfig, error) {
	config := []*APIKeyConfig{}
	var configErrors ConfigErrors

	envConfig := strings.TrimSpace(os.Getenv(APIKeyEnvVarName))
	if envConfig == "" {
		configErrors.errs = append(configErrors.errs, fmt.Errorf("environment variable %s must be set", APIKeyEnvVarName))
		return nil, configErrors
	}

	err := json.Unmarshal([]byte(envConfig), &config)
	if err != nil {
		configErrors.errs = append(configErrors.errs, fmt.Errorf("error parsing %s: %s", APIKeyEnvVarName, err))
		return nil, configErrors
	}

	for _, entry := range config {
		if strings.TrimSpace(entry.Principal) == "" {
			configErrors.errs = append(configErrors.errs, fmt.Errorf("%s entry principal string cannot be empty", APIKeyEnvVarName))
		}
		if len(entry.Keys) == 0 {
			configErrors.errs = append(configErrors.errs, fmt.Errorf("%s entry keys array cannot be empty for principal %s", APIKeyEnvVarName, entry.Principal))
		}
		for _, key := range entry.Keys {
			k := strings.TrimSpace(key)
			if k == "" {
				configErrors.errs = append(configErrors.errs, fmt.Errorf("%s key string cannot be empty for principal %s", APIKeyEnvVarName, entry.Principal))
				continue
			}
			if len(k) < 16 {
				configErrors.errs = append(configErrors.errs, fmt.Errorf("%s key string length cannot be less that 16 characters for principal %s", APIKeyEnvVarName, entry.Principal))
			}
		}
	}

	if len(configErrors.errs) > 0 {
		return nil, configErrors
	}

	return config, nil
}
