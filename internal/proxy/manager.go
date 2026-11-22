package proxy

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"cyronmanager/internal/secure"
	"cyronmanager/internal/store"
	"cyronmanager/internal/types"
)

type healthState struct {
	failures    int
	lastFailure time.Time
	lastSuccess time.Time
	lastLatency int64
	lastExitIP  string
}

type Manager struct {
	store *store.Store

	mu       sync.RWMutex
	profiles map[string]types.ProxyProfile
	health   map[string]healthState
}

func NewManager(st *store.Store) *Manager {
	m := &Manager{
		store:    st,
		profiles: make(map[string]types.ProxyProfile),
		health:   make(map[string]healthState),
	}
	_ = m.Reload()
	return m
}

func (m *Manager) Reload() error {
	if m.store == nil {
		return errors.New("proxy store is not ready")
	}
	profiles, err := m.store.ListProxyProfiles()
	if err != nil {
		return err
	}
	next := make(map[string]types.ProxyProfile, len(profiles))
	failed := 0
	for _, profile := range profiles {
		if profile.Password != "" {
			plain, err := secure.Unprotect(profile.Password)
			if err != nil {
				failed++
				profile.LoadError = "رمز ذخیره‌شدهٔ این پروفایل خوانده نشد؛ آن را دوباره وارد کن"
				profile.Password = ""
			} else {
				profile.Password = plain
			}
		}
		next[profile.ID] = profile
	}
	m.mu.Lock()
	m.profiles = next
	m.mu.Unlock()
	if failed > 0 {
		return fmt.Errorf("%d proxy profile password(s) could not be decrypted", failed)
	}
	return nil
}

func (m *Manager) List() []types.ProxyProfileView {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]types.ProxyProfileView, 0, len(m.profiles))
	for _, profile := range m.profiles {
		out = append(out, ProfileView(profile))
	}
	sortProfiles(out)
	return out
}

func (m *Manager) Get(id string) (types.ProxyProfile, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	profile, ok := m.profiles[strings.TrimSpace(id)]
	return profile, ok
}

func (m *Manager) Save(input types.ProxyProfileInput) (types.ProxyProfileView, error) {
	profile, err := NormalizeProfile(input)
	if err != nil {
		return types.ProxyProfileView{}, err
	}
	if profile.ID == "" {
		profile.ID = NewID()
	}
	m.mu.RLock()
	existing, exists := m.profiles[profile.ID]
	m.mu.RUnlock()
	if profile.Password == "" && exists {
		profile.Password = existing.Password
	}
	now := time.Now().UnixMilli()
	if exists {
		profile.CreatedAt = existing.CreatedAt
	} else {
		profile.CreatedAt = now
	}
	profile.UpdatedAt = now
	if m.store == nil {
		return types.ProxyProfileView{}, errors.New("proxy store is not ready")
	}
	if err := m.store.SaveProxyProfile(&profile); err != nil {
		return types.ProxyProfileView{}, err
	}
	m.mu.Lock()
	m.profiles[profile.ID] = profile
	m.mu.Unlock()
	return ProfileView(profile), nil
}

func (m *Manager) Delete(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("شناسه پروفایل خالی است")
	}
	if m.store == nil {
		return errors.New("proxy store is not ready")
	}
	if err := m.store.DeleteProxyProfile(id); err != nil {
		return err
	}
	m.mu.Lock()
	delete(m.profiles, id)
	delete(m.health, id)
	m.mu.Unlock()
	return nil
}

func (m *Manager) Test(ctx context.Context, id, testURL string) (types.ProxyTestResult, error) {
	profile, ok := m.Get(id)
	if !ok {
		return types.ProxyTestResult{}, sql.ErrNoRows
	}
	result := TestProfile(ctx, profile, testURL)
	m.recordTest(result)
	return result, nil
}

func (m *Manager) TestInput(ctx context.Context, input types.ProxyProfileInput, testURL string) (types.ProxyTestResult, error) {
	profile, err := NormalizeProfile(input)
	if err != nil {
		return types.ProxyTestResult{}, err
	}
	if profile.Password == "" {
		m.mu.RLock()
		existing, ok := m.profiles[profile.ID]
		m.mu.RUnlock()
		if ok {
			profile.Password = existing.Password
		}
	}
	if profile.ID == "" {
		profile.ID = "new"
	}
	result := TestProfile(ctx, profile, testURL)
	return result, nil
}

func (m *Manager) recordTest(result types.ProxyTestResult) {
	id := strings.TrimSpace(result.ProfileID)
	if id == "" || id == "new" {
		return
	}
	m.mu.Lock()
	state := m.health[id]
	if result.OK {
		state.failures = 0
		state.lastSuccess = time.Now()
		state.lastLatency = result.LatencyMS
		state.lastExitIP = result.ExitIP
	} else {
		state.failures++
		state.lastFailure = time.Now()
	}
	m.health[id] = state
	m.mu.Unlock()
}

func (m *Manager) Healthy(id string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	state, ok := m.health[id]
	if !ok {
		return true
	}
	if state.failures < 3 {
		return true
	}
	return time.Since(state.lastFailure) > 2*time.Minute
}

func (m *Manager) MarkFailure(id string) {
	m.mu.Lock()
	state := m.health[id]
	state.failures++
	state.lastFailure = time.Now()
	m.health[id] = state
	m.mu.Unlock()
}

func (m *Manager) MarkSuccess(id string) {
	m.mu.Lock()
	state := m.health[id]
	state.failures = 0
	state.lastSuccess = time.Now()
	m.health[id] = state
	m.mu.Unlock()
}

func (m *Manager) Resolve(task types.Task, settings types.Settings) (*RoutePlan, error) {
	plan := &RoutePlan{
		Bypass:      ParseBypass(settings.ProxyBypass),
		ApplyBypass: true,
		Healthy:     m.Healthy,
		Timeout:     20 * time.Second,
	}
	mode := strings.ToLower(strings.TrimSpace(task.ProxyMode))
	switch mode {
	case types.ProxyModeDirect:
		plan.Routes = []Route{{Direct: true}}
		plan.ApplyBypass = false
		return plan, nil
	case types.ProxyModeProfile:
		profileID := strings.TrimSpace(task.ProxyProfileID)
		if profileID == "" {
			return nil, errors.New("پروفایل پروکسی برای این دانلود انتخاب نشده است")
		}
		profile, ok := m.Get(profileID)
		if !ok {
			return nil, errors.New("پروفایل پروکسی انتخاب‌شده پیدا نشد")
		}
		plan.Routes = []Route{{Profile: profile}}
		plan.ApplyBypass = false
		return plan, nil
	default:
		plan.ApplyBypass = true
		if strings.TrimSpace(settings.ProxyDefaultID) == "" {
			plan.Routes = []Route{{Direct: true}}
			return plan, nil
		}
		primary, ok := m.Get(settings.ProxyDefaultID)
		if !ok {
			return nil, errors.New("پروفایل پیش‌فرض پروکسی پیدا نشد")
		}
		plan.Routes = []Route{{Profile: primary}}
		seen := map[string]bool{primary.ID: true}
		for _, id := range settings.ProxyFallbackIDs {
			id = strings.TrimSpace(id)
			if id == "" || seen[id] {
				continue
			}
			profile, ok := m.Get(id)
			if !ok {
				continue
			}
			seen[id] = true
			plan.Routes = append(plan.Routes, Route{Profile: profile})
		}
		if settings.ProxyAllowDirectFallback {
			plan.Routes = append(plan.Routes, Route{Direct: true})
		}
		return plan, nil
	}
}

func (m *Manager) ProfileName(id string) string {
	if profile, ok := m.Get(id); ok {
		return profile.Name
	}
	return ""
}

// FindByEndpoint returns an existing profile that matches the given endpoint so
// repeated imports of the same legacy proxy string do not create duplicates.
func (m *Manager) FindByEndpoint(protocol, host string, port int, username string) (types.ProxyProfileView, bool) {
	protocol = strings.ToLower(strings.TrimSpace(protocol))
	host = normalizeHost(host)
	username = strings.TrimSpace(username)
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, profile := range m.profiles {
		if profile.Protocol != protocol || profile.Host != host || profile.Port != port {
			continue
		}
		if profile.Username != username {
			continue
		}
		return ProfileView(profile), true
	}
	return types.ProxyProfileView{}, false
}

func (m *Manager) MigrateLegacy(settings *types.Settings) (bool, error) {
	if settings == nil {
		return false, nil
	}
	if strings.TrimSpace(settings.Proxy) == "" {
		return false, nil
	}
	m.mu.RLock()
	hasProfiles := len(m.profiles) > 0
	m.mu.RUnlock()
	if hasProfiles {
		return false, nil
	}
	input, err := ParseLegacyProxy(settings.Proxy)
	if err != nil {
		return false, err
	}
	view, err := m.Save(input)
	if err != nil {
		return false, err
	}
	settings.ProxyDefaultID = view.ID
	settings.Proxy = ""
	return true, nil
}

func sortProfiles(profiles []types.ProxyProfileView) {
	for i := 1; i < len(profiles); i++ {
		for j := i; j > 0 && strings.ToLower(profiles[j].Name) < strings.ToLower(profiles[j-1].Name); j-- {
			profiles[j], profiles[j-1] = profiles[j-1], profiles[j]
		}
	}
}
