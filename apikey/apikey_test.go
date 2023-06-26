package apikey

import (
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
)

var api = &APIKey{
	Config: []*APIKeyConfig{
		{
			Principal: "dev-team",
			Keys:      []string{"07H3R73573RK3Y23", "abcdef0123456789"},
		},
		{
			Principal: "prod-team",
			Keys:      []string{"3133773573RK3Y42"},
		},
	},
}

func TestValidate(t *testing.T) {
	testCases := []struct {
		Name              string
		Header            http.Header
		ExpectedPrincipal string
		ExpectedStatus    int
		IsValid           bool
	}{
		{
			Name:           "Missing header",
			ExpectedStatus: http.StatusUnauthorized,
		},
		{
			Name: "Basic auth header",
			Header: http.Header{
				"Authorization": []string{"Basic QWxhZGRpbjpvcGVuIHNlc2FtZQ=="},
			},
			ExpectedStatus: http.StatusBadRequest,
		},
		{
			Name: "Incomplete header",
			Header: http.Header{
				"Authorization": []string{"Bear"},
			},
			ExpectedStatus: http.StatusBadRequest,
		},
		{
			Name: "Missing key",
			Header: http.Header{
				"Authorization": []string{"Bearer "},
			},
			ExpectedStatus: http.StatusUnprocessableEntity,
		},
		{
			Name: "Good key 1",
			Header: http.Header{
				"Authorization": []string{"Bearer 07H3R73573RK3Y23"},
			},
			ExpectedStatus:    http.StatusOK,
			ExpectedPrincipal: "dev-team",
			IsValid:           true,
		},
		{
			Name: "Good key 2",
			Header: http.Header{
				"Authorization": []string{"Bearer abcdef0123456789"},
			},
			ExpectedStatus:    http.StatusOK,
			ExpectedPrincipal: "dev-team",
			IsValid:           true,
		},
		{
			Name: "Good key 3",
			Header: http.Header{
				"Authorization": []string{"Bearer   3133773573RK3Y42"},
			},
			ExpectedStatus:    http.StatusOK,
			ExpectedPrincipal: "prod-team",
			IsValid:           true,
		},
		{
			Name: "Good key 4",
			Header: http.Header{
				"Authorization": []string{"Bearer 3133773573RK3Y42"},
			},
			ExpectedStatus:    http.StatusOK,
			ExpectedPrincipal: "prod-team",
			IsValid:           true,
		},
		{
			Name: "Wrong key capitalization 1",
			Header: http.Header{
				"Authorization": []string{"Bearer 07h3r73573rk3y23"},
			},
			ExpectedStatus: http.StatusUnprocessableEntity,
		},
		{
			Name: "Wrong key capitalization 2",
			Header: http.Header{
				"Authorization": []string{"Bearer 3133773573rk3y42"},
			},
			ExpectedStatus: http.StatusUnprocessableEntity,
		},
		{
			Name: "Wrong bearer capitalization 1",
			Header: http.Header{
				"Authorization": []string{"bearer 3133773573RK3Y42"},
			},
			ExpectedStatus: http.StatusBadRequest,
		},
		{
			Name: "Wrong bearer capitalization 2",
			Header: http.Header{
				"Authorization": []string{"BEARER 3133773573RK3Y42"},
			},
			ExpectedStatus: http.StatusBadRequest,
		},
		{
			Name: "Good key prefix",
			Header: http.Header{
				"Authorization": []string{"Bearer 31337"},
			},
			ExpectedStatus: http.StatusUnprocessableEntity,
		},
		{
			Name: "Good key plus extra",
			Header: http.Header{
				"Authorization": []string{"Bearer 3133773573RK3Y42C0RRUPT3D"},
			},
			ExpectedStatus: http.StatusUnprocessableEntity,
		},
		{
			Name: "Multiple keys in one header",
			Header: http.Header{
				"Authorization": []string{"Bearer 3133773573RK3Y42 07H3R73573RK3Y23"},
			},
			ExpectedStatus: http.StatusUnprocessableEntity,
		},
		{
			Name: "Multiple Authorization headers, correct one last",
			Header: http.Header{
				"Authorization": []string{"Bearer x", "Bearer y", "Bearer 07H3R73573RK3Y23"},
			},
			ExpectedStatus: http.StatusUnprocessableEntity,
		},
		{
			Name: "Multiple Authorization headers, correct one first",
			Header: http.Header{
				"Authorization": []string{"Bearer 3133773573RK3Y42", "Bearer x", "Bearer y"},
			},
			ExpectedStatus:    http.StatusOK,
			ExpectedPrincipal: "prod-team",
			IsValid:           true,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.Name, func(t *testing.T) {
			req := &http.Request{
				Header: testCase.Header,
			}
			apiKeyResult := api.Validate(req)
			if apiKeyResult.IsValid() != testCase.IsValid {
				t.Errorf("Expected IsValid to return %v but got %v", testCase.IsValid, apiKeyResult.IsValid())
			}
			if apiKeyResult.StatusCode != testCase.ExpectedStatus {
				t.Errorf("Expected status %d but Validate returned %d", testCase.ExpectedStatus, apiKeyResult.StatusCode)
			}
			if testCase.ExpectedPrincipal != "" && testCase.ExpectedPrincipal != apiKeyResult.Principal {
				t.Errorf("Expected key %s but got %s", testCase.ExpectedPrincipal, apiKeyResult.Principal)
			}
		})
	}
}

const expectedText string = "It works!"
const healthText string = "healthy"

func testHandleFunc(w http.ResponseWriter, r *http.Request) {
	fmt.Fprint(w, expectedText)
}

func healthHandleFunc(w http.ResponseWriter, r *http.Request) {
	fmt.Fprint(w, healthText)
}

const testURL string = "/test"

func TestNewServeMuxRestricted(t *testing.T) {
	testCases := []struct {
		Name           string
		Header         http.Header
		ExpectedStatus int
		ExpectedText   string
		URL            string
	}{
		{
			Name:           "Restricted Missing Header",
			ExpectedStatus: http.StatusUnauthorized,
			ExpectedText:   ErrAuthorizationRequired.Error(),
			URL:            testURL,
		},
		{
			Name: "Restricted Invalid API Key",
			Header: http.Header{
				"Authorization": []string{"Bearer 8B4DF00D"},
			},
			ExpectedStatus: http.StatusUnprocessableEntity,
			ExpectedText:   ErrInvalidAPIKey.Error(),
			URL:            testURL,
		},
		{
			Name: "Restricted Valid API Key",
			Header: http.Header{
				"Authorization": []string{"Bearer 3133773573RK3Y42"},
			},
			ExpectedStatus: http.StatusOK,
			ExpectedText:   expectedText,
			URL:            testURL,
		},
		{
			Name: "Restricted Valid API Key Bad Path",
			Header: http.Header{
				"Authorization": []string{"Bearer 3133773573RK3Y42"},
			},
			ExpectedStatus: http.StatusNotFound,
			ExpectedText:   "404 page not found",
			URL:            "/unknown",
		},
		{
			Name: "Restricted Invalid API Key Bad Path",
			Header: http.Header{
				"Authorization": []string{"Bearer 0123456789012345"},
			},
			ExpectedStatus: http.StatusUnprocessableEntity,
			ExpectedText:   "invalid API key",
			URL:            "/unknown",
		},
		{
			Name:           "Restricted No API Key Health Check",
			ExpectedStatus: http.StatusOK,
			ExpectedText:   healthText,
			URL:            "/health",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.Name, func(t *testing.T) {
			url, err := url.Parse(testCase.URL)
			if err != nil {
				t.Errorf("failed to parse test case URL %q", testCase.URL)
			}
			r := &http.Request{
				Method: http.MethodGet,
				Header: testCase.Header,
				URL:    url,
			}
			w := httptest.NewRecorder()

			mux := http.NewServeMux()
			mux.HandleFunc("/test", testHandleFunc)
			mux.HandleFunc("/health", healthHandleFunc)
			wrapped := api.ValidateHandler(mux)
			wrapped.ServeHTTP(w, r)

			resp := w.Result()

			if resp.StatusCode != testCase.ExpectedStatus {
				t.Errorf("Expected status code %d, but got %d", testCase.ExpectedStatus, resp.StatusCode)
			}
			buf, err := ioutil.ReadAll(resp.Body)
			if err != nil {
				t.Errorf("error reading error response body for %q - %s", r.URL, err)
			}
			bodyText := strings.TrimSpace(string(buf))
			if testCase.ExpectedText != "" && bodyText != testCase.ExpectedText {
				t.Errorf("Expected response text to be %q, but got %q", testCase.ExpectedText, bodyText)
			}
		})
	}
}

func TestLoadConfigFromEnvJSON(t *testing.T) {
	previousEnvConfig := strings.TrimSpace(os.Getenv(APIKeyEnvVarName))
	if previousEnvConfig != "" {
		defer func() {
			os.Setenv(APIKeyEnvVarName, previousEnvConfig)
		}()
	}

	tests := []struct {
		Name          string
		Config        string
		ExpectedError string
		NumErrors     int
	}{
		{
			Name:   "Valid configuration",
			Config: `[{"principal": "ia-team","keys": ["ABCDEFGHIJKLMNOP"]}]`,
		},
		{
			Name:          "Invalid configuration empty principal",
			Config:        `[{"principal": "","keys": ["ABCDEFGHIJKLMNOP"]}]`,
			ExpectedError: fmt.Sprintf("%s entry principal string cannot be empty", APIKeyEnvVarName),
			NumErrors:     1,
		},
		{
			Name:          "Invalid configuration empty keyss array",
			Config:        `[{"principal": "ia-team","keys": []}]`,
			ExpectedError: fmt.Sprintf("%s entry keys array cannot be empty for principal ia-team", APIKeyEnvVarName),
			NumErrors:     1,
		},
		{
			Name:          "Invalid configuration empty key",
			Config:        `[{"principal": "ia-team","keys": [""]}]`,
			ExpectedError: fmt.Sprintf("%s key string cannot be empty for principal ia-team", APIKeyEnvVarName),
			NumErrors:     1,
		},
		{
			Name:          "Invalid configuration short key length",
			Config:        `[{"principal": "ia-team","keys": ["ABCDEF"]}]`,
			ExpectedError: fmt.Sprintf("%s key string length cannot be less that 16 characters for principal ia-team", APIKeyEnvVarName),
			NumErrors:     1,
		},
		{
			Name:          "Invalid configuration empty environment variable",
			ExpectedError: fmt.Sprintf("environment variable %s must be set", APIKeyEnvVarName),
			NumErrors:     1,
		},
		{
			Name:          "Invalid configuration JSON parse failure", // missing closing quote at end of ia-team
			Config:        `[{"principal": "ia-team,"keys": ["ABCDEFGHIJKLMNOP"]}]`,
			ExpectedError: fmt.Sprintf("error parsing %s: invalid character 'k' after object key:value pair", APIKeyEnvVarName),
			NumErrors:     1,
		},
		{
			Name:          "Invalid configuration short key length and empty key",
			Config:        `[{"principal": "ia-team","keys": ["ABCDEF", ""]}]`,
			ExpectedError: strings.Join([]string{fmt.Sprintf("%s key string length cannot be less that 16 characters for principal ia-team", APIKeyEnvVarName), fmt.Sprintf("%s key string cannot be empty for principal ia-team", APIKeyEnvVarName)}, ", "),
			NumErrors:     2,
		},
	}
	log.SetOutput(ioutil.Discard) // test but prevent log output from errs.LogErrors()
	for _, test := range tests {
		t.Run(test.Name, func(t *testing.T) {
			os.Setenv(APIKeyEnvVarName, test.Config)
			_, errs := GetConfigFromEnvJSON()
			if errs != nil {
				if errs.Error() != test.ExpectedError {
					t.Errorf("error expected %q, error result %q", test.ExpectedError, errs.Error())
				}
				numErrors := len(errs.(ConfigErrors).GetErrors())
				if numErrors != test.NumErrors {
					t.Errorf("%d error expected, errors received %d", test.NumErrors, numErrors)
				}
				errs.(ConfigErrors).LogErrors()
			}
			if test.ExpectedError != "" && errs == nil {
				t.Errorf("error expected %q but no error returned", test.ExpectedError)
			}

		})
	}
}
