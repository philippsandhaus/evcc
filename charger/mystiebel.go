package charger

// LICENSE

// Copyright (c) evcc.io & contributors

// This module is NOT covered by the MIT license. All rights reserved.

// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.

// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/evcc-io/evcc/api"
	"github.com/evcc-io/evcc/api/implement"
	"github.com/evcc-io/evcc/charger/mystiebel"
	"github.com/evcc-io/evcc/util"
)

func init() {
	registry.AddCtx("mystiebel", NewMyStiebelFromConfig)
}

// MyStiebel controls a Stiebel Eltron heat pump via the MyStiebel cloud.
type MyStiebel struct {
	*embed
	implement.Caps
	log    *util.Logger
	client *mystiebel.Client
	mode   int64
}

// NewMyStiebelFromConfig creates a MyStiebel charger from config.
func NewMyStiebelFromConfig(ctx context.Context, other map[string]any) (api.Charger, error) {
	cc := struct {
		embed          `mapstructure:",squash"`
		User           string
		Password       string
		ClientID       string
		InstallationID string
		Cache          time.Duration
	}{
		embed: embed{
			Icon_:     "heatpump",
			Features_: []api.Feature{api.Continuous, api.Heating, api.IntegratedDevice},
		},
		Cache: time.Minute,
	}

	if err := util.DecodeOther(other, &cc); err != nil {
		return nil, err
	}

	if cc.User == "" || cc.Password == "" {
		return nil, api.ErrMissingCredentials
	}

	log := util.NewLogger("mystiebel").Redact(cc.User, cc.Password)

	auth := mystiebel.NewAuth(log, cc.User, cc.Password, cc.ClientID)

	installationID := cc.InstallationID
	if installationID == "" {
		installations, err := auth.Installations(ctx)
		if err != nil {
			return nil, fmt.Errorf("get installations: %w", err)
		}
		if len(installations) == 0 {
			return nil, fmt.Errorf("no installations found for this account")
		}
		if len(installations) > 1 {
			ids := make([]string, len(installations))
			for i, inst := range installations {
				ids[i] = fmt.Sprintf("%d (%s)", inst.ID, inst.Profile.Name)
			}
			return nil, fmt.Errorf("multiple installations found, specify installation_id: %v", ids)
		}
		installationID = strconv.FormatInt(installations[0].ID, 10)
		log.DEBUG.Printf("using installation %s (%s)", installationID, installations[0].Profile.Name)
	}

	client := mystiebel.NewClient(log, auth, installationID)

	res := &MyStiebel{
		embed:  &cc.embed,
		Caps:   implement.New(),
		log:    log,
		client: client,
		mode:   Normal,
	}

	// Decorate with temperature (maps to api.Battery.Soc).
	implement.Has(res, implement.Battery(util.Cached(func() (float64, error) {
		return client.GetFloat(context.Background(), mystiebel.RegisterDomeTemp)
	}, cc.Cache)))

	// Decorate with target temperature (maps to api.SocLimiter.GetLimitSoc).
	implement.Has(res, implement.SocLimiter(func() (int64, error) {
		v, err := client.GetFloat(context.Background(), mystiebel.RegisterTargetTemp)
		return int64(v), err
	}))

	return res, nil
}

// Status implements the api.Charger interface.
func (m *MyStiebel) Status() (api.ChargeStatus, error) {
	mode, err := m.getMode()
	if err != nil {
		return api.StatusNone, err
	}

	if mode == Boost {
		return api.StatusC, nil
	}
	return api.StatusB, nil
}

// Enabled implements the api.Charger interface.
func (m *MyStiebel) Enabled() (bool, error) {
	mode, err := m.getMode()
	return mode == Boost, err
}

// Enable implements the api.Charger interface.
func (m *MyStiebel) Enable(enable bool) error {
	if enable {
		return m.setMode(Boost)
	}
	return m.setMode(Normal)
}

// MaxCurrent implements the api.Charger interface.
func (m *MyStiebel) MaxCurrent(_ int64) error {
	return nil
}

// getMode reads the boost request register to determine the current mode.
func (m *MyStiebel) getMode() (int64, error) {
	v, err := m.client.GetFloat(context.Background(), mystiebel.RegisterBoostRequest)
	if err != nil {
		// Fall back to cached mode on transient read errors.
		m.log.WARN.Printf("getMode: %v — using cached mode %d", err, m.mode)
		return m.mode, nil
	}

	// Register 2382: 0=off (normal), 1=active via app (boost), 2=active via BT
	if int64(v) >= 1 {
		m.mode = Boost
	} else {
		m.mode = Normal
	}
	return m.mode, nil
}

// setMode writes the boost request register.
func (m *MyStiebel) setMode(mode int64) error {
	value := int64(0)
	if mode == Boost {
		value = 1 // Sofortausladung via APP
	}

	if err := m.client.SetValue(context.Background(), mystiebel.RegisterBoostRequest, value); err != nil {
		return fmt.Errorf("setMode: %w", err)
	}

	m.mode = mode
	return nil
}
