package azure

// https://docs.microsoft.com/en-us/azure/active-directory/develop/access-tokens for details about the tokens

import (
	"context"
	_ "crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
)

type AzureAD struct {
	AzureADConfig  AzureADConfig
	provider       *oidc.Provider
	providerMu     sync.Mutex
	openIDConfig   *OpenIDConfig
	openIDConfigMu sync.Mutex
}

type AzureADConfig struct {
	StaticAssetsVersion int64
	RedirectURL         string
	ClientID            string
	Host                string
	TenantID            string
}

func GetConfigFromENV() (AzureADConfig, error) {
	invalid := []string{}

	readFromENV := func(dest *string, key string) {
		*dest = os.Getenv(key)
		if *dest == "" {
			invalid = append(invalid, fmt.Sprintf("environment variable %q cannot be empty", key))
		}
	}

	config := AzureADConfig{}

	readFromENV(&config.ClientID, "AZURE_AD_CLIENT_ID")
	readFromENV(&config.Host, "AZURE_AD_HOST")
	readFromENV(&config.RedirectURL, "AZURE_AD_REDIRECT_URL")
	readFromENV(&config.TenantID, "AZURE_AD_TENANT_ID")

	if len(invalid) > 0 {
		return AzureADConfig{}, fmt.Errorf(strings.Join(invalid, ", "))
	}

	return config, nil
}

type JWTBody struct {
	Audience          string   `json:"aud"` // Azure AD Client ID
	IssuerURL         string   `json:"iss"`
	IssuedAt          int64    `json:"iat"` // unix timestamp
	NotBefore         int64    `json:"nbf"` // unix timestamp
	Expiry            int64    `json:"exp"` // unix timestamp
	Email             string   `json:"email"`
	Groups            []string `json:"groups"` // AD group names
	Name              string   `json:"name"`
	Nonce             string   `json:"nonce"`
	ObjectID          string   `json:"oid"`
	PreferredUsername string   `json:"preferred_username"` // EUA@cloud.cms.gov
	Rh                string   `json:"rh"`
	SID               string   `json:"sid"`
	Subject           string   `json:"sub"`
	TenantID          string   `json:"tid"`
	Username          string   `json:"upn"` // EUA@cloud.cms.gov",
	UTI               string   `json:"uti"`
	Version           string   `json:"ver"`
}

// OpenIDConfig is not a complete representation of the payload, only added fields of interest
// see https://login.microsoftonline.us/7c8bb92a-832a-4d12-8e7e-c569d7b232c9/.well-known/openid-configuration
// for more details
type OpenIDConfig struct {
	JWKSURI          string    `json:"jwks_uri"`
	CheckSession     string    `json:"check_session_iframe"`
	UserInfoEndpoint string    `json:"userinfo_endpoint"`
	MSGraphHost      string    `json:"msgraph_host"`
	cacheExpiry      time.Time `json:"-"`
}

const (
	TokenBody = 1
)

var maxAgeRegex = regexp.MustCompile(`^max-age=(\d+)`)

// initial caller must check and record the nonce in caller to prevent replay attacks
func (aad *AzureAD) VerifyToken(token string) (*JWTBody, error) {
	tokenParts := strings.Split(token, ".")
	if len(tokenParts) != 3 {
		return nil, fmt.Errorf("invalid number of JWT token segments %d, should be 3", len(tokenParts))
	}

	bodyBytes, err := base64.RawStdEncoding.DecodeString(tokenParts[TokenBody])
	if err != nil {
		return nil, fmt.Errorf("error decoding JWT body: %s", err)
	}
	body := JWTBody{}
	if err := json.Unmarshal(bodyBytes, &body); err != nil {
		return nil, fmt.Errorf("error unmarshalling JWT body json: %s", err)
	}

	provider, err := aad.GetProvider()
	if err != nil {
		return nil, err
	}
	verifier := provider.Verifier(&oidc.Config{
		ClientID: aad.AzureADConfig.ClientID,
	})
	_, err = verifier.Verify(context.Background(), token)
	if err != nil {
		return nil, fmt.Errorf("failed to verify ID token: %s", err)
	}

	return &body, nil
}

// GetOpenIDConfig from Microsoft for CMSs tenant ID
func (aad *AzureAD) GetOpenIDConfig() (*OpenIDConfig, error) {
	aad.openIDConfigMu.Lock()
	defer aad.openIDConfigMu.Unlock()

	if aad.openIDConfig != nil && time.Now().Before(aad.openIDConfig.cacheExpiry) {
		return aad.openIDConfig, nil
	}

	openIDConfig := &OpenIDConfig{}
	openIDConfigURL := fmt.Sprintf("https://login.microsoftonline.us/%s/.well-known/openid-configuration", aad.AzureADConfig.TenantID)

	resp, err := http.Get(openIDConfigURL)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch OpenID config from %s: %s", openIDConfigURL, err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to fetch OpenID config from %s: status: %s", openIDConfigURL, resp.Status)
	}

	cacheContolHeader := resp.Header.Get("Cache-control")
	matches := maxAgeRegex.FindStringSubmatch(cacheContolHeader)
	var maxAgeSeconds int64
	if len(matches) == 2 {
		maxAgeSeconds, err = strconv.ParseInt(matches[1], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("failed to convert %s to an int64: %s", matches[1], err)
		}
	}

	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if err := json.Unmarshal(body, &openIDConfig); err != nil {
		return nil, fmt.Errorf("error decoding OpenIDConfig: %s", err)
	}

	openIDConfig.cacheExpiry = time.Now().Add(time.Second * time.Duration(maxAgeSeconds))
	aad.openIDConfig = openIDConfig

	return aad.openIDConfig, nil
}

func (aad *AzureAD) GetProvider() (*oidc.Provider, error) {
	aad.providerMu.Lock()
	defer aad.providerMu.Unlock()

	if aad.provider == nil {
		issuerURL := fmt.Sprintf("https://login.microsoftonline.us/%s/v2.0", aad.AzureADConfig.TenantID)
		provider, err := oidc.NewProvider(context.Background(), issuerURL)
		if err != nil {
			return nil, fmt.Errorf("failed to instantiate provider: %s", err)
		}
		aad.provider = provider
	}

	return aad.provider, nil
}
