package main

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type versionChoicesMsg struct {
	Key    string
	Values []string
	Err    error
}

func versionField(key string) bool {
	return key == "game-build" || key == "artifact" || key == "kora-tag"
}
func versionChoices(key string, catalogSource string, platform string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		s := newSources()
		v := versionChoicesMsg{Key: key}
		switch key {
		case "game-build":
			v.Values = []string{"latest"}
			builds, err := s.builds(ctx)
			v.Err = err
			for _, b := range builds {
				v.Values = append(v.Values, strconv.Itoa(b))
			}
		case "artifact":
			v.Values = []string{"recommended"}
			address, err := s.artifact(ctx, "recommended", platform)
			v.Err = err
			if err == nil {
				v.Values = append(v.Values, address)
			}
		case "kora-tag":
			if catalogSource != "" {
				v.Values = []string{"latest"}
				catalog, _, err := loadCatalog(ctx, s.Client, catalogSource, defaultCatalogURL, fallbackCatalogURL)
				v.Err = err
				for _, resource := range catalog.Resources {
					if resource.ID == "kora-core" || strings.Contains(resource.Repository, "/kora_core") {
						for _, release := range resource.Versions {
							v.Values = append(v.Values, release.Tag)
						}
						break
					}
				}
				return v
			}
			v.Values = []string{"latest", "default", "recipe"}
			tags, err := s.tags(ctx, "KoraFiveM/kora_core")
			v.Err = err
			v.Values = append(v.Values, tags...)
		}
		return v
	}
}
func (m model) updateVersions(msg tea.Msg) (model, tea.Cmd, bool) {
	if v, ok := msg.(versionChoicesMsg); ok {
		if m.stage != "version-select" || fields[m.step].Key != v.Key {
			return m, nil, true
		}
		m.versionOptions = v.Values
		m.versionLoading = false
		m.choice = 0
		for i, value := range v.Values {
			if value == m.value(v.Key) {
				m.choice = i
			}
		}
		if v.Err != nil {
			m.err = v.Err.Error()
		}
		return m, nil, true
	}
	key, ok := msg.(tea.KeyPressMsg)
	if !ok || m.stage != "version-select" {
		return m, nil, false
	}
	switch key.String() {
	case "up", "left":
		m.choice = max(0, m.choice-1)
	case "down", "right", "tab":
		m.choice = min(max(0, len(m.versionOptions)-1), m.choice+1)
	case "r":
		m.versionLoading = true
		m.err = ""
		return m, versionChoices(fields[m.step].Key, m.config.CatalogSource, targetPlatform(m.config)), true
	case "c":
		m.stage = "form"
		m.err = ""
		return m, m.input.Focus(), true
	case "enter":
		if len(m.versionOptions) > 0 {
			if err := m.set(fields[m.step].Key, m.versionOptions[m.choice]); err != nil {
				m.err = err.Error()
				return m, nil, true
			}
			m.step++
			return m, m.next(), true
		}
	case "esc":
		return m, m.back(), true
	default:
		return m, nil, false
	}
	return m, nil, true
}
func (m model) versionView() string {
	out := title.Render(fields[m.step].Title) + "\n\n"
	if m.versionLoading {
		out += m.spinner.View() + " Loading available versions…\n"
	}
	start := max(0, m.choice-3)
	end := min(len(m.versionOptions), start+7)
	for i := start; i < end; i++ {
		prefix := "  "
		if i == m.choice {
			prefix = "› "
		}
		value := m.versionOptions[i]
		if len(value) > 65 {
			value = "…" + value[len(value)-64:]
		}
		out += accent.Render(prefix+value) + "\n"
	}
	out += "\n" + dim.Render(fmt.Sprintf("%d choices · latest is resolved separately for each Kora repo", len(m.versionOptions)))
	if fields[m.step].Key == "kora-tag" {
		out += "\n" + dim.Render("recipe preserves the YAML's explicit refs; other choices override Kora resource refs.")
	}
	return strings.TrimSpace(out)
}
