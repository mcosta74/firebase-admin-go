// Copyright 2019 Google Inc. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"firebase.google.com/go/internal"
)

const (
	providerConfigEndpoint = "https://identitytoolkit.googleapis.com/v2beta1"

	idpEntityIDKey = "idpConfig.idpEntityId"
	ssoURLKey      = "idpConfig.ssoUrl"
	signRequestKey = "idpConfig.signRequest"
	idpCertsKey    = "idpConfig.idpCertificates"

	spEntityIDKey  = "spConfig.spEntityId"
	callbackURIKey = "spConfig.callbackUri"

	displayNameKey = "displayName"
	enabledKey     = "enabled"
)

type nestedMap map[string]interface{}

func (nm nestedMap) Get(key string) (interface{}, bool) {
	segments := strings.Split(key, ".")
	curr := map[string]interface{}(nm)
	for idx, segment := range segments {
		val, ok := curr[segment]
		if idx == len(segments)-1 || !ok {
			return val, ok
		}

		curr = val.(map[string]interface{})
	}

	return nil, false
}

func (nm nestedMap) GetString(key string) (string, bool) {
	if val, ok := nm.Get(key); ok {
		return val.(string), true
	}

	return "", false
}

func (nm nestedMap) Set(key string, value interface{}) {
	segments := strings.Split(key, ".")
	curr := map[string]interface{}(nm)
	for idx, segment := range segments {
		if idx == len(segments)-1 {
			curr[segment] = value
			return
		}

		child, ok := curr[segment]
		if ok {
			curr = child.(map[string]interface{})
			continue
		}
		newChild := make(map[string]interface{})
		curr[segment] = newChild
		curr = newChild
	}
}

func (nm nestedMap) UpdateMask() ([]string, error) {
	return buildMask(nm), nil
}

func buildMask(data map[string]interface{}) []string {
	var mask []string
	for k, v := range data {
		if child, ok := v.(map[string]interface{}); ok {
			childMask := buildMask(child)
			for _, item := range childMask {
				mask = append(mask, fmt.Sprintf("%s.%s", k, item))
			}
		} else {
			mask = append(mask, k)
		}
	}

	return mask
}

// SAMLProviderConfig is the SAML auth provider configuration.
// See http://docs.oasis-open.org/security/saml/Post2.0/sstc-saml-tech-overview-2.0.html.
type SAMLProviderConfig struct {
	ID                    string
	DisplayName           string
	Enabled               bool
	IDPEntityID           string
	SSOURL                string
	RequestSigningEnabled bool
	X509Certificates      []string
	RPEntityID            string
	CallbackURL           string
}

// SAMLProviderConfigToCreate represents the options used to create a new SAMLProviderConfig.
type SAMLProviderConfigToCreate struct {
	id     string
	params nestedMap
}

// ID sets the provider ID of the new config.
func (config *SAMLProviderConfigToCreate) ID(id string) *SAMLProviderConfigToCreate {
	config.id = id
	return config
}

// IDPEntityID sets the IDPEntityID field of the new config.
func (config *SAMLProviderConfigToCreate) IDPEntityID(entityID string) *SAMLProviderConfigToCreate {
	return config.set(idpEntityIDKey, entityID)
}

// SSOURL sets the SSOURL field of the new config.
func (config *SAMLProviderConfigToCreate) SSOURL(url string) *SAMLProviderConfigToCreate {
	return config.set(ssoURLKey, url)
}

// RequestSigningEnabled enables or disables the request signing support.
func (config *SAMLProviderConfigToCreate) RequestSigningEnabled(enabled bool) *SAMLProviderConfigToCreate {
	return config.set(signRequestKey, enabled)
}

// X509Certificates sets the certificates for the new config.
func (config *SAMLProviderConfigToCreate) X509Certificates(certs []string) *SAMLProviderConfigToCreate {
	var result []idpCertificate
	for _, cert := range certs {
		result = append(result, idpCertificate{cert})
	}

	return config.set(idpCertsKey, result)
}

// RPEntityID sets the RPEntityID field of the new config.
func (config *SAMLProviderConfigToCreate) RPEntityID(entityID string) *SAMLProviderConfigToCreate {
	return config.set(spEntityIDKey, entityID)
}

// CallbackURL sets the CallbackURL field of the new config.
func (config *SAMLProviderConfigToCreate) CallbackURL(url string) *SAMLProviderConfigToCreate {
	return config.set(callbackURIKey, url)
}

// DisplayName sets the DisplayName field of the new config.
func (config *SAMLProviderConfigToCreate) DisplayName(name string) *SAMLProviderConfigToCreate {
	return config.set(displayNameKey, name)
}

// Enabled enables or disables the new config.
func (config *SAMLProviderConfigToCreate) Enabled(enabled bool) *SAMLProviderConfigToCreate {
	return config.set(enabledKey, enabled)
}

func (config *SAMLProviderConfigToCreate) set(key string, value interface{}) *SAMLProviderConfigToCreate {
	if config.params == nil {
		config.params = make(nestedMap)
	}

	config.params.Set(key, value)
	return config
}

func (config *SAMLProviderConfigToCreate) buildRequest() (nestedMap, string, error) {
	if err := validateSAMLConfigID(config.id); err != nil {
		return nil, "", err
	}

	if len(config.params) == 0 {
		return nil, "", errors.New("no parameters specified in the create request")
	}

	if val, ok := config.params.GetString(idpEntityIDKey); !ok || val == "" {
		return nil, "", errors.New("IDPEntityID must not be empty")
	}

	if val, ok := config.params.GetString(ssoURLKey); !ok || val == "" {
		return nil, "", errors.New("SSOURL must not be empty")
	} else if _, err := url.ParseRequestURI(val); err != nil {
		return nil, "", fmt.Errorf("failed to parse SSOURL: %v", err)
	}

	var certs interface{}
	var ok bool
	if certs, ok = config.params.Get(idpCertsKey); !ok || len(certs.([]idpCertificate)) == 0 {
		return nil, "", errors.New("X509Certificates must not be empty")
	}
	for _, cert := range certs.([]idpCertificate) {
		if cert.X509Certificate == "" {
			return nil, "", errors.New("X509Certificates must not contain empty strings")
		}
	}

	if val, ok := config.params.GetString(spEntityIDKey); !ok || val == "" {
		return nil, "", errors.New("RPEntityID must not be empty")
	}

	if val, ok := config.params.GetString(callbackURIKey); !ok || val == "" {
		return nil, "", errors.New("CallbackURL must not be empty")
	} else if _, err := url.ParseRequestURI(val); err != nil {
		return nil, "", fmt.Errorf("failed to parse CallbackURL: %v", err)
	}

	return config.params, config.id, nil
}

// SAMLProviderConfigToUpdate represents the options used to update an existing SAMLProviderConfig.
type SAMLProviderConfigToUpdate struct {
	params nestedMap
}

// IDPEntityID the IDPEntityID field of the config.
func (config *SAMLProviderConfigToUpdate) IDPEntityID(entityID string) *SAMLProviderConfigToUpdate {
	return config.set(idpEntityIDKey, entityID)
}

// SSOURL updates the SSOURL field of the config.
func (config *SAMLProviderConfigToUpdate) SSOURL(url string) *SAMLProviderConfigToUpdate {
	return config.set(ssoURLKey, url)
}

// RequestSigningEnabled enables or disables the request signing support.
func (config *SAMLProviderConfigToUpdate) RequestSigningEnabled(enabled bool) *SAMLProviderConfigToUpdate {
	return config.set(signRequestKey, enabled)
}

// X509Certificates updates the certificates of the config.
func (config *SAMLProviderConfigToUpdate) X509Certificates(certs []string) *SAMLProviderConfigToUpdate {
	var result []idpCertificate
	for _, cert := range certs {
		result = append(result, idpCertificate{cert})
	}

	return config.set(idpCertsKey, result)
}

// RPEntityID updates the RPEntityID field of the config.
func (config *SAMLProviderConfigToUpdate) RPEntityID(entityID string) *SAMLProviderConfigToUpdate {
	return config.set(spEntityIDKey, entityID)
}

// CallbackURL updates the CallbackURL field of the config.
func (config *SAMLProviderConfigToUpdate) CallbackURL(url string) *SAMLProviderConfigToUpdate {
	return config.set(callbackURIKey, url)
}

// DisplayName updates the DisplayName field of the config.
func (config *SAMLProviderConfigToUpdate) DisplayName(name string) *SAMLProviderConfigToUpdate {
	var nameOrNil interface{}
	if name != "" {
		nameOrNil = name
	}

	return config.set(displayNameKey, nameOrNil)
}

// Enabled enables or disables the new config.
func (config *SAMLProviderConfigToUpdate) Enabled(enabled bool) *SAMLProviderConfigToUpdate {
	return config.set(enabledKey, enabled)
}

func (config *SAMLProviderConfigToUpdate) set(key string, value interface{}) *SAMLProviderConfigToUpdate {
	if config.params == nil {
		config.params = make(nestedMap)
	}

	config.params.Set(key, value)
	return config
}

func (config *SAMLProviderConfigToUpdate) buildRequest() (nestedMap, error) {
	if len(config.params) == 0 {
		return nil, errors.New("no parameters specified in the update request")
	}

	if val, ok := config.params.GetString(idpEntityIDKey); ok && val == "" {
		return nil, errors.New("IDPEntityID must not be empty")
	}

	if val, ok := config.params.GetString(ssoURLKey); ok {
		if val == "" {
			return nil, errors.New("SSOURL must not be empty")
		}
		if _, err := url.ParseRequestURI(val); err != nil {
			return nil, fmt.Errorf("failed to parse SSOURL: %v", err)
		}
	}

	if val, ok := config.params.Get(idpCertsKey); ok {
		if len(val.([]idpCertificate)) == 0 {
			return nil, errors.New("X509Certificates must not be empty")
		}
		for _, cert := range val.([]idpCertificate) {
			if cert.X509Certificate == "" {
				return nil, errors.New("X509Certificates must not contain empty strings")
			}
		}
	}

	if val, ok := config.params.GetString(spEntityIDKey); ok && val == "" {
		return nil, errors.New("RPEntityID must not be empty")
	}

	if val, ok := config.params.GetString(callbackURIKey); ok {
		if val == "" {
			return nil, errors.New("CallbackURL must not be empty")
		}
		if _, err := url.ParseRequestURI(val); err != nil {
			return nil, fmt.Errorf("failed to parse CallbackURL: %v", err)
		}
	}

	return config.params, nil
}

type providerConfigClient struct {
	endpoint   string
	projectID  string
	httpClient *internal.HTTPClient
}

func newProviderConfigClient(hc *http.Client, conf *internal.AuthConfig) *providerConfigClient {
	client := &internal.HTTPClient{
		Client:      hc,
		SuccessFn:   internal.HasSuccessStatus,
		CreateErrFn: handleHTTPError,
		Opts: []internal.HTTPOption{
			internal.WithHeader("X-Client-Version", fmt.Sprintf("Go/Admin/%s", conf.Version)),
		},
	}
	return &providerConfigClient{
		endpoint:   providerConfigEndpoint,
		projectID:  conf.ProjectID,
		httpClient: client,
	}
}

// SAMLProviderConfig returns the SAMLProviderConfig with the given ID.
func (c *providerConfigClient) SAMLProviderConfig(ctx context.Context, id string) (*SAMLProviderConfig, error) {
	if err := validateSAMLConfigID(id); err != nil {
		return nil, err
	}

	req := &internal.Request{
		Method: http.MethodGet,
		URL:    fmt.Sprintf("/inboundSamlConfigs/%s", id),
	}
	var result samlProviderConfigDAO
	if _, err := c.makeRequest(ctx, req, &result); err != nil {
		return nil, err
	}

	return result.toSAMLProviderConfig(), nil
}

// CreateSAMLProviderConfig creates a new SAML provider config from the given parameters.
func (c *providerConfigClient) CreateSAMLProviderConfig(ctx context.Context, config *SAMLProviderConfigToCreate) (*SAMLProviderConfig, error) {
	if config == nil {
		return nil, errors.New("config must not be nil")
	}

	body, id, err := config.buildRequest()
	if err != nil {
		return nil, err
	}

	req := &internal.Request{
		Method: http.MethodPost,
		URL:    "/inboundSamlConfigs",
		Body:   internal.NewJSONEntity(body),
		Opts: []internal.HTTPOption{
			internal.WithQueryParam("inboundSamlConfigId", id),
		},
	}
	var result samlProviderConfigDAO
	if _, err := c.makeRequest(ctx, req, &result); err != nil {
		return nil, err
	}

	return result.toSAMLProviderConfig(), nil
}

// UpdateSAMLProviderConfig updates an existing SAML provider config with the given parameters.
func (c *providerConfigClient) UpdateSAMLProviderConfig(ctx context.Context, id string, config *SAMLProviderConfigToUpdate) (*SAMLProviderConfig, error) {
	if err := validateSAMLConfigID(id); err != nil {
		return nil, err
	}
	if config == nil {
		return nil, errors.New("config must not be nil")
	}

	body, err := config.buildRequest()
	if err != nil {
		return nil, err
	}

	mask, err := body.UpdateMask()
	if err != nil {
		return nil, fmt.Errorf("failed to construct update mask: %v", err)
	}

	req := &internal.Request{
		Method: http.MethodPatch,
		URL:    fmt.Sprintf("/inboundSamlConfigs/%s", id),
		Body:   internal.NewJSONEntity(body),
		Opts: []internal.HTTPOption{
			internal.WithQueryParam("updateMask", strings.Join(mask, ",")),
		},
	}
	var result samlProviderConfigDAO
	if _, err := c.makeRequest(ctx, req, &result); err != nil {
		return nil, err
	}

	return result.toSAMLProviderConfig(), nil
}

// DeleteSAMLProviderConfig deletes the SAMLProviderConfig with the given ID.
func (c *providerConfigClient) DeleteSAMLProviderConfig(ctx context.Context, id string) error {
	if err := validateSAMLConfigID(id); err != nil {
		return err
	}

	req := &internal.Request{
		Method: http.MethodDelete,
		URL:    fmt.Sprintf("/inboundSamlConfigs/%s", id),
	}
	_, err := c.makeRequest(ctx, req, nil)
	return err
}

func (c *providerConfigClient) makeRequest(ctx context.Context, req *internal.Request, v interface{}) (*internal.Response, error) {
	if c.projectID == "" {
		return nil, errors.New("project id not available")
	}

	req.URL = fmt.Sprintf("%s/projects/%s%s", c.endpoint, c.projectID, req.URL)
	return c.httpClient.DoAndUnmarshal(ctx, req, v)
}

type idpCertificate struct {
	X509Certificate string `json:"x509Certificate"`
}

type samlProviderConfigDAO struct {
	Name      string `json:"name"`
	IDPConfig struct {
		IDPEntityID     string           `json:"idpEntityId"`
		SSOURL          string           `json:"ssoUrl"`
		IDPCertificates []idpCertificate `json:"idpCertificates"`
		SignRequest     bool             `json:"signRequest"`
	} `json:"idpConfig"`
	SPConfig struct {
		SPEntityID  string `json:"spEntityId"`
		CallbackURI string `json:"callbackUri"`
	} `json:"spConfig"`
	DisplayName string `json:"displayName"`
	Enabled     bool   `json:"enabled"`
}

func (dao *samlProviderConfigDAO) toSAMLProviderConfig() *SAMLProviderConfig {
	var certs []string
	for _, cert := range dao.IDPConfig.IDPCertificates {
		certs = append(certs, cert.X509Certificate)
	}

	return &SAMLProviderConfig{
		ID:                    extractResourceID(dao.Name),
		DisplayName:           dao.DisplayName,
		Enabled:               dao.Enabled,
		IDPEntityID:           dao.IDPConfig.IDPEntityID,
		SSOURL:                dao.IDPConfig.SSOURL,
		RequestSigningEnabled: dao.IDPConfig.SignRequest,
		X509Certificates:      certs,
		RPEntityID:            dao.SPConfig.SPEntityID,
		CallbackURL:           dao.SPConfig.CallbackURI,
	}
}

func validateSAMLConfigID(id string) error {
	if !strings.HasPrefix(id, "saml.") {
		return fmt.Errorf("invalid SAML provider id: %q", id)
	}

	return nil
}

func extractResourceID(name string) string {
	// name format: "projects/project-id/resource/resource-id"
	segments := strings.Split(name, "/")
	return segments[len(segments)-1]
}