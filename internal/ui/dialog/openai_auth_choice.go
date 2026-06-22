package dialog

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/catwalk/pkg/catwalk"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/ui/common"
	uv "github.com/charmbracelet/ultraviolet"
)

const OpenAIAuthChoiceID = "openai_auth_choice"

type OpenAIAuthChoice struct {
	com          *common.Common
	isOnboarding bool

	provider  catwalk.Provider
	model     config.SelectedModel
	modelType config.SelectedModelType

	selected int
	help     help.Model

	keyMap struct {
		UpDown key.Binding
		Submit key.Binding
		Close  key.Binding
	}
}

var _ Dialog = (*OpenAIAuthChoice)(nil)

func NewOpenAIAuthChoice(com *common.Common, isOnboarding bool, provider catwalk.Provider, model config.SelectedModel, modelType config.SelectedModelType) (*OpenAIAuthChoice, tea.Cmd) {
	m := &OpenAIAuthChoice{
		com:          com,
		isOnboarding: isOnboarding,
		provider:     provider,
		model:        model,
		modelType:    modelType,
	}
	m.help = help.New()
	m.help.Styles = com.Styles.DialogHelpStyles()
	m.keyMap.UpDown = key.NewBinding(key.WithKeys("up", "down"), key.WithHelp("↑/↓", "choose"))
	m.keyMap.Submit = key.NewBinding(key.WithKeys("enter", "ctrl+y"), key.WithHelp("enter", "select"))
	m.keyMap.Close = CloseKey
	return m, nil
}

func (m *OpenAIAuthChoice) ID() string { return OpenAIAuthChoiceID }

func (m *OpenAIAuthChoice) HandleMsg(msg tea.Msg) Action {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch {
		case key.Matches(msg, m.keyMap.UpDown):
			if m.selected == 0 {
				m.selected = 1
			} else {
				m.selected = 0
			}
		case key.Matches(msg, m.keyMap.Submit):
			method := "browser"
			if m.selected == 1 {
				method = "api_key"
			}
			return ActionSelectOpenAIAuthMethod{Method: method, Provider: m.provider, Model: m.model, ModelType: m.modelType}
		case key.Matches(msg, m.keyMap.Close):
			return ActionClose{}
		}
	}
	return nil
}

func (m *OpenAIAuthChoice) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	t := m.com.Styles
	view := lipgloss.NewStyle().Width(60).Render(strings.Join([]string{
		common.DialogTitle(t, t.Dialog.Title.Render("How do you want to authenticate with OpenAI?"), 60, t.Dialog.TitleGradFromColor, t.Dialog.TitleGradToColor),
		m.body(),
		m.help.View(m),
	}, "\n"))
	if m.isOnboarding {
		DrawOnboarding(scr, area, view)
	} else {
		DrawCenter(scr, area, view)
	}
	return nil
}

func (m *OpenAIAuthChoice) body() string {
	t := m.com.Styles
	options := []struct {
		label string
		desc  string
	}{
		{label: "Browser login", desc: "Use the OpenAI subscription browser flow."},
		{label: "API key", desc: "Enter an OpenAI API key instead."},
	}
	parts := make([]string, 0, len(options))
	for i, option := range options {
		prefix := "  "
		if i == m.selected {
			prefix = "> "
		}
		parts = append(parts, lipgloss.NewStyle().Bold(i == m.selected).Render(fmt.Sprintf("%s%s", prefix, option.label))+"\n"+t.Dialog.SecondaryText.Render("  "+option.desc))
	}
	return strings.Join(parts, "\n\n")
}

func (m *OpenAIAuthChoice) FullHelp() [][]key.Binding { return [][]key.Binding{m.ShortHelp()} }

func (m *OpenAIAuthChoice) ShortHelp() []key.Binding {
	return []key.Binding{m.keyMap.UpDown, m.keyMap.Submit, m.keyMap.Close}
}
