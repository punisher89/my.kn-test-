package pilot

import (
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

type ServicePreset struct {
	Aliases  []string `json:"aliases"`
	Profile  Profile  `json:"profile"`
	Category string   `json:"category"`
	Note     string   `json:"note"`
}

//go:embed service-presets.json
var servicePresetJSON []byte
var servicePresets = func() []ServicePreset {
	var presets []ServicePreset
	if err := json.Unmarshal(servicePresetJSON, &presets); err != nil {
		panic(err)
	}
	return presets
}()

func ServicePresets() []ServicePreset {
	result := append([]ServicePreset{}, servicePresets...)
	for i := range result {
		result[i].Aliases = append([]string{}, result[i].Aliases...)
		result[i].Profile.Domains = append([]string{}, result[i].Profile.Domains...)
		result[i].Profile.SourceExclusions = append([]string{}, result[i].Profile.SourceExclusions...)
		result[i].Profile.Targets = append([]Target{}, result[i].Profile.Targets...)
	}
	return result
}

func presetDomains(id string) []string {
	for _, preset := range servicePresets {
		if preset.Profile.ID == id {
			return append([]string{}, preset.Profile.Domains...)
		}
	}
	return nil
}

func (m *Manager) AddService(id string) error {
	if !m.op.TryLock() {
		return ErrBusy
	}
	defer m.op.Unlock()
	c := m.Config()
	for _, p := range c.Profiles {
		if p.ID == id {
			return fmt.Errorf("сервис %s уже добавлен; существующий профиль сохранён", id)
		}
	}
	for _, preset := range ServicePresets() {
		if preset.Profile.ID != id {
			continue
		}
		c.Profiles = append(c.Profiles, preset.Profile)
		return m.changeLocked(c)
	}
	return errors.New("сервис отсутствует в каталоге")
}

func (m *Manager) RemoveService(id string) error {
	if !m.op.TryLock() {
		return ErrBusy
	}
	defer m.op.Unlock()
	c := m.Config()
	for i, p := range c.Profiles {
		if p.ID != id {
			continue
		}
		c.Profiles = append(c.Profiles[:i], c.Profiles[i+1:]...)
		return m.changeLocked(c)
	}
	return errors.New("профиль не найден")
}

func (m *Manager) AddCustomService(p Profile) error {
	p.Name = strings.TrimSpace(p.Name)
	if p.Name == "" {
		return errors.New("задайте название сервиса")
	}
	if !m.op.TryLock() {
		return ErrBusy
	}
	defer m.op.Unlock()
	if p.ID == "" {
		b := make([]byte, 4)
		if _, e := rand.Read(b); e != nil {
			return e
		}
		p.ID = "custom-" + hex.EncodeToString(b)
	}
	if p.Strategy == "" {
		p.Strategy = "none"
	}
	if len(p.Targets) == 0 {
		return errors.New("нужен контрольный HTTPS-адрес")
	}
	for _, t := range p.Targets {
		if t.Contains == "" {
			return errors.New("задайте контрольный фрагмент ответа")
		}
	}
	c := m.Config()
	c.Profiles = append(c.Profiles, p)
	return m.changeLocked(c)
}
