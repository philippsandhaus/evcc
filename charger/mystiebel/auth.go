package mystiebel

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/evcc-io/evcc/util"
	"github.com/evcc-io/evcc/util/request"
	"github.com/google/uuid"
)

// Auth handles JWT authentication against the MyStiebel REST API.
type Auth struct {
	log      *util.Logger
	helper   *request.Helper
	username string
	password string
	clientID string

	token   string
	expires time.Time
}

// NewAuth creates an Auth instance. If clientID is empty, a random UUID is generated.
func NewAuth(log *util.Logger, username, password, clientID string) *Auth {
	if clientID == "" {
		clientID = uuid.New().String()
		log.DEBUG.Printf("generated client UUID: %s", clientID)
	}
	return &Auth{
		log:      log,
		helper:   request.NewHelper(log),
		username: username,
		password: password,
		clientID: clientID,
	}
}

func (a *Auth) baseHeaders() map[string]string {
	return map[string]string{
		"X-SC-ClientApp-Name":    AppName,
		"X-SC-ClientApp-Version": AppVersion,
		"User-Agent":             UserAgent,
		"Content-Type":           "application/json; charset=utf-8",
	}
}

func (a *Auth) applyHeaders(req *http.Request, extra map[string]string) {
	for k, v := range a.baseHeaders() {
		req.Header.Set(k, v)
	}
	for k, v := range extra {
		req.Header.Set(k, v)
	}
}

// Login authenticates and stores the JWT token.
func (a *Auth) Login(ctx context.Context) error {
	payload := loginRequest{
		UserName:   a.username,
		Password:   a.password,
		ClientID:   a.clientID,
		RememberMe: true,
	}

	req, err := request.New(http.MethodPost, BaseURL+"/api/v1/Jwt/login", request.MarshalJSON(payload), nil)
	if err != nil {
		return err
	}
	a.applyHeaders(req, nil)

	var res loginResponse
	if err := a.helper.DoJSON(req.WithContext(ctx), &res); err != nil {
		return fmt.Errorf("login: %w", err)
	}
	if res.Token == "" {
		return fmt.Errorf("login succeeded but no token received")
	}

	a.token = res.Token
	if exp, err := jwtExpiry(res.Token); err == nil {
		a.expires = exp
	} else {
		a.expires = time.Now().Add(23 * time.Hour)
	}

	a.log.DEBUG.Println("login successful, token expires", a.expires.Format(time.RFC3339))
	return nil
}

// EnsureToken refreshes the token if it expires within 5 minutes.
func (a *Auth) EnsureToken(ctx context.Context) error {
	if a.token == "" || time.Until(a.expires) < 5*time.Minute {
		return a.Login(ctx)
	}
	return nil
}

// ClientID returns the UUID used as the OAuth client identifier.
func (a *Auth) ClientID() string {
	return a.clientID
}

// Token returns the current JWT, logging in first if needed.
func (a *Auth) Token(ctx context.Context) (string, error) {
	if err := a.EnsureToken(ctx); err != nil {
		return "", err
	}
	return a.token, nil
}

// Installations returns all installations for the authenticated user.
func (a *Auth) Installations(ctx context.Context) ([]Installation, error) {
	if err := a.EnsureToken(ctx); err != nil {
		return nil, err
	}

	payload := installationsRequest{IncludeWithPendingUserAccesses: true}
	req, err := request.New(http.MethodPost, ServiceURL+"/api/v1/InstallationsInfo/own", request.MarshalJSON(payload), nil)
	if err != nil {
		return nil, err
	}
	a.applyHeaders(req, map[string]string{"Authorization": "Bearer " + a.token})

	var res installationsResponse
	if err := a.helper.DoJSON(req.WithContext(ctx), &res); err != nil {
		return nil, fmt.Errorf("get installations: %w", err)
	}

	return res.Items, nil
}
