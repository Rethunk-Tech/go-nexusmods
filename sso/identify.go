package sso

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"

	"github.com/Rethunk-Tech/go-httpkit"
)

// Identity is the account an access token belongs to.
type Identity struct {
	ID      int
	Name    string
	Premium bool
}

// Identify asks Nexus who an access token belongs to. The API's /v1/users/validate.json answers personal API keys
// only and refuses an access token.
func (o OAuth) Identify(ctx context.Context, access string) (Identity, error) {
	if err := o.checkBase(); err != nil {
		return Identity{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, o.base()+"/userinfo", http.NoBody)
	if err != nil {
		return Identity{}, err
	}
	req.Header.Set("Authorization", "Bearer "+access)
	req.Header.Set("Accept", "application/json")
	body, err := httpkit.DoCapped(o.client(), req, maxTokenBody, nil)
	if err != nil {
		return Identity{}, fmt.Errorf("nexus userinfo: %w", err)
	}
	var raw struct {
		Sub   string   `json:"sub"`
		Name  string   `json:"name"`
		Roles []string `json:"membership_roles"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return Identity{}, fmt.Errorf("nexus userinfo: %w", err)
	}
	id, err := strconv.Atoi(raw.Sub)
	if err != nil {
		return Identity{}, fmt.Errorf("nexus userinfo: account id %q: %w", raw.Sub, err)
	}
	premium := slices.Contains(raw.Roles, "premium") || slices.Contains(raw.Roles, "lifetimepremium")
	return Identity{ID: id, Name: raw.Name, Premium: premium}, nil
}
