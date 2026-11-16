package pilot

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
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
		result[i].Profile.Targets = append([]Target{}, result[i].Profile.Targets...)
	}
	return result
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
