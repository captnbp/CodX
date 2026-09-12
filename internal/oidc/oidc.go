// Package oidc implements OIDC authentication for CodX: provider discovery,
// the OAuth2 authorization-code flow, and extraction of user identity (subject,
// username, groups) from verified ID tokens.
package oidc

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/captnbp/CodX/internal/config"
	"github.com/captnbp/CodX/internal/session"
	"github.com/captnbp/CodX/internal/slug"
	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// Authenticator wraps the OIDC provider and OAuth2 config.
type Authenticator struct {
	provider    *oidc.Provider
	oauthConfig *oauth2.Config
	verifier    *oidc.IDTokenVerifier
	cfg         config.OIDCConfig
}

// New creates an Authenticator by discovering the OIDC provider configuration.
func New(ctx context.Context, cfg config.OIDCConfig) (*Authenticator, error) {
	provider, err := oidc.NewProvider(ctx, cfg.Issuer)
	if err != nil {
		return nil, fmt.Errorf("discover OIDC provider %q: %w", cfg.Issuer, err)
	}

	oauthConfig := &oauth2.Config{
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		RedirectURL:  cfg.RedirectURL,
		Endpoint:     provider.Endpoint(),
		Scopes:       []string{oidc.ScopeOpenID, "profile", "email", "groups"},
	}

	verifier := provider.Verifier(&oidc.Config{ClientID: cfg.ClientID})

	return &Authenticator{
		provider:    provider,
		oauthConfig:  oauthConfig,
		verifier:    verifier,
		cfg:         cfg,
	}, nil
}

// AuthURL generates the authorization URL with a random state parameter.
func (a *Authenticator) AuthURL(state string) string {
	return a.oauthConfig.AuthCodeURL(state, oauth2.AccessTypeOnline)
}

// Exchange exchanges the authorization code for tokens and creates a Session
// from the verified ID token.
func (a *Authenticator) Exchange(ctx context.Context, code string) (*session.Session, error) {
	token, err := a.oauthConfig.Exchange(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("exchange authorization code: %w", err)
	}

	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok {
		return nil, errors.New("no id_token in token response")
	}

	idToken, err := a.verifier.Verify(ctx, rawIDToken)
	if err != nil {
		return nil, fmt.Errorf("verify id_token: %w", err)
	}

	// Extract claims.
	claims, err := extractClaims(idToken, a.cfg.GroupClaimName, a.cfg.UsernameClaimName)
	if err != nil {
		return nil, fmt.Errorf("extract claims: %w", err)
	}

	sessID, err := randomID(32)
	if err != nil {
		return nil, fmt.Errorf("generate session ID: %w", err)
	}

	userSlug := slug.Make(claims.Username, 63)

	return &session.Session{
		ID:           sessID,
		Subject:      idToken.Subject,
		Username:     claims.Username,
		Slug:         userSlug,
		Groups:       claims.Groups,
		IsAdmin:      containsGroup(claims.Groups, a.cfg.AdminGroup),
		AccessToken:  token.AccessToken,
		RefreshToken: token.RefreshToken,
		IDToken:      rawIDToken,
		ExpiresAt:    token.Expiry,
	}, nil
}

// IsAdmin checks whether the given groups contain the admin group.
func (a *Authenticator) IsAdmin(groups []string) bool {
	return containsGroup(groups, a.cfg.AdminGroup)
}

// Provider returns the underlying OIDC provider (used for endpoint discovery).
func (a *Authenticator) Provider() *oidc.Provider {
	return a.provider
}

// RandomState generates a cryptographically random state parameter.
func RandomState() (string, error) {
	return randomID(32)
}

func randomID(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// extractedClaims holds the parsed ID token claims.
type extractedClaims struct {
	Subject  string
	Username string
	Groups   []string
}

func extractClaims(idToken *oidc.IDToken, groupClaim, usernameClaim string) (*extractedClaims, error) {
	// Parse all claims into a raw map to support flexible claim names.
	var raw map[string]any
	if err := idToken.Claims(&raw); err != nil {
		return nil, fmt.Errorf("unmarshal claims: %w", err)
	}

	claims := &extractedClaims{
		Subject: idToken.Subject,
	}

	// Username.
	if v, ok := raw[usernameClaim]; ok {
		if s, ok := v.(string); ok {
			claims.Username = s
		}
	}
	if claims.Username == "" {
		return nil, fmt.Errorf("username claim %q not found or not a string", usernameClaim)
	}

	// Groups — the claim can be a []string or []any.
	claims.Groups = extractGroups(raw, groupClaim)

	return claims, nil
}

func extractGroups(raw map[string]any, claim string) []string {
	v, ok := raw[claim]
	if !ok {
		return nil
	}
	switch val := v.(type) {
	case []string:
		return val
	case []any:
		groups := make([]string, 0, len(val))
		for _, g := range val {
			if s, ok := g.(string); ok {
				groups = append(groups, s)
			}
		}
		return groups
	case string:
		return []string{val}
	default:
		return nil
	}
}

func containsGroup(groups []string, target string) bool {
	for _, g := range groups {
		if g == target {
			return true
		}
	}
	return false
}

// TimeNow is overridable for testing.
var TimeNow = time.Now
