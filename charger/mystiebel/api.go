package mystiebel

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const (
	BaseURL    = "https://auth.mystiebel.com"
	ServiceURL = "https://serviceapi.mystiebel.com"
	WSURL      = "wss://serviceapi.mystiebel.com/ws/v1"

	AppName    = "MyStiebelApp"
	AppVersion = "Android_2.3.0"
	UserAgent  = "MyStiebelApp/2.3.0 Dalvik/2.1.0"
)

// Register indices for DHW control.
const (
	RegisterBoostRequest    = int64(2382) // WWK_BoostType: 0=off, 1=active (app), 2=active (BT)
	RegisterHotWaterPlus    = int64(2487) // Hot Water Plus request: 0/1
	RegisterSGReadyState    = int64(2388) // SG-Ready state (read-only)
	RegisterDomeTemp        = int64(15)   // Current DHW dome temperature (°C)
	RegisterTargetTemp      = int64(2378) // Current target temperature (°C)
	RegisterSetpointComfort = int64(13)   // Setpoint comfort temperature (°C)
	RegisterSetpointEco     = int64(14)   // Setpoint eco temperature (°C)
)

// loginRequest is the payload for /api/v1/Jwt/login.
type loginRequest struct {
	UserName   string `json:"userName"`
	Password   string `json:"password"`
	ClientID   string `json:"clientId"`
	RememberMe bool   `json:"rememberMe"`
}

// loginResponse is the response from /api/v1/Jwt/login.
type loginResponse struct {
	Token string `json:"token"`
}

// installationsRequest is the payload for /api/v1/InstallationsInfo/own.
type installationsRequest struct {
	IncludeWithPendingUserAccesses bool `json:"includeWithPendingUserAccesses"`
}

// InstallationProfile contains device model info.
type InstallationProfile struct {
	Name string `json:"name"`
}

// Installation holds the info returned per device.
type Installation struct {
	ID         int64               `json:"id"`
	Profile    InstallationProfile `json:"profile"`
	MACAddress string              `json:"macAddress"`
}

// installationsResponse wraps the list of installations.
type installationsResponse struct {
	Items []Installation `json:"items"`
}

// rpcRequest is a JSON-RPC 2.0 request.
type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params"`
}

// rpcResponse is a JSON-RPC 2.0 response.
type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *rpcError       `json:"error"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string {
	return fmt.Sprintf("rpc error %d: %s", e.Code, e.Message)
}

// loginParams is the params for the WebSocket Login method.
type loginParams struct {
	ClientID string `json:"clientId"`
	JWT      string `json:"jwt"`
}

// getValuesParams is the params for getValues.
type getValuesParams struct {
	InstallationID string  `json:"installationId"`
	Fields         []int64 `json:"fields"`
}

// setValuesParams is the params for setValues.
type setValuesParams struct {
	InstallationID          string       `json:"installationId"`
	UUID                    string       `json:"UUID"`
	ListenWithValuesChanged bool         `json:"listenWithValuesChanged"`
	Fields                  []fieldValue `json:"fields"`
}

// fieldValue is a register index + value pair.
type fieldValue struct {
	RegisterIndex int64 `json:"registerIndex"`
	DisplayValue  any   `json:"displayValue"`
}

// getValuesResult is the result of getValues.
type getValuesResult struct {
	Fields []fieldValue `json:"fields"`
}

// jwtExpiry extracts the expiry timestamp from a JWT token.
func jwtExpiry(token string) (time.Time, error) {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return time.Time{}, fmt.Errorf("invalid JWT format")
	}

	decoded, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return time.Time{}, fmt.Errorf("decode JWT payload: %w", err)
	}

	var payload struct {
		Exp int64 `json:"exp"`
	}
	if err := json.Unmarshal(decoded, &payload); err != nil {
		return time.Time{}, fmt.Errorf("parse JWT payload: %w", err)
	}
	if payload.Exp == 0 {
		return time.Time{}, fmt.Errorf("no exp claim in JWT")
	}

	return time.Unix(payload.Exp, 0), nil
}
