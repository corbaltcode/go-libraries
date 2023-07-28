package azure

import (
	"encoding/json"
	"fmt"
	"net/http"
)

type GraphGroups struct {
	Items []Items `json:"value"`
}
type Items struct {
	Name string `json:"displayName"`
}

func (gg GraphGroups) Has(name string) bool {
	for _, group := range gg.Items {
		if group.Name == name {
			return true
		}
	}

	return false
}

func (aad *AzureAD) GetGraphGroups(accessToken string) (*GraphGroups, error) {
	openIDConfig, err := aad.GetOpenIDConfig()
	if err != nil {
		return nil, err
	}

	url := fmt.Sprintf("https://%s/beta/me/memberOf/microsoft.graph.group?$orderby=displayName&$select=displayName", openIDConfig.MSGraphHost)

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to fetch user groups: %s", resp.Status)
	}

	defer resp.Body.Close()

	groups := &GraphGroups{}

	err = json.NewDecoder(resp.Body).Decode(groups)

	return groups, err
}
