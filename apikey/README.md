# API Key Library

## Configuration

The `API_KEY_CONFIG` environment variable must be set to valid JSON.

### Example JSON Configurations

For request validation the configuration must be defined with a `principal` (a user, team, or application identifier) and any number of `keys` entries.
```
[
    {
        "principal": "ia-team",
        "keys": ["50m3r4nd0m4P1k3Y", "..."]
    }
]
```

Load the configuration and instantiate the module.

 ```
config, err := apikey.GetConfigFromEnvJSON() // handle err

apiKey := apikey.APIKey{Config: config}
 ```

The err returned by `apikey.GetConfigFromEnvJSON()` is of the `apikey.ConfigErrors` type. It satifies the error interface so it can be treated as a normal error, but it also has methods to fetch or log the individual errors if that works better for your application. See `ConfigErrors.GetErrors()` and `ConfigErrors.LogErrors()` in config.go.


## Usage
### http.Handler (optional)
`apikey.ValidateHandler()` is an http.Handler that will intercept all requests to your application except for the `/health` endpoint. This will keep invalid API key requests from reaching your application logic. This is be useful as outlined in the first configuration example above.

```
http.ListenAndServe(port, apiKey.ValidateHandler(mux))
```

See examples in apikey_test.go#`TestNewServeMuxRestricted` and cmd/creds-api/main.go.

### API Key Validation

If your application requires a more direct handling of the API key or role validation pass the `*http.Request` to `apiKey.Validate()`.

```
func handleEndpoint(w http.ResponseWriter, r *http.Request) {
    result := apiKey.Validate(r)
    ...
}
```
The `Result` struct provides access to the principal, http status code, and error, if any.

See apikey.go for more details.
