package dialog

import (
	"context"
	"fmt"
	"strings"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/catwalk/pkg/catwalk"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/oauth"
	openaioauth "github.com/charmbracelet/crush/internal/oauth/openai"
	"github.com/charmbracelet/crush/internal/ui/common"
	"github.com/charmbracelet/crush/internal/ui/util"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/pkg/browser"
)

const OAuthOpenAIID = "oauth_openai"

type OAuthOpenAI struct {
	com          *common.Common
	isOnboarding bool

	provider  catwalk.Provider
	model     config.SelectedModel
	modelType config.SelectedModelType

	State OAuthState
	url   string
	sess  *openaioauth.BrowserSession
	token *oauth.Token

	spinner spinner.Model
	help    help.Model
	keyMap  struct {
		Copy   key.Binding
		Submit key.Binding
		Close  key.Binding
	}
	width      int
	cancelFunc context.CancelFunc
}

var _ Dialog = (*OAuthOpenAI)(nil)

func NewOAuthOpenAI(com *common.Common, isOnboarding bool, provider catwalk.Provider, model config.SelectedModel, modelType config.SelectedModelType) (*OAuthOpenAI, tea.Cmd, error) {
	m := &OAuthOpenAI{
		com:          com,
		isOnboarding: isOnboarding,
		provider:     provider,
		model:        model,
		modelType:    modelType,
		width:        60,
		State:        OAuthStateInitializing,
	}

	t := com.Styles
	m.spinner = spinner.New(spinner.WithSpinner(spinner.Dot), spinner.WithStyle(t.Dialog.OAuth.Spinner))
	m.help = help.New()
	m.help.Styles = t.DialogHelpStyles()
	m.keyMap.Copy = key.NewBinding(key.WithKeys("c"), key.WithHelp("c", "copy link"))
	m.keyMap.Submit = key.NewBinding(key.WithKeys("enter", "ctrl+y"), key.WithHelp("enter", "copy & open"))
	m.keyMap.Close = CloseKey

	sess, err := openaioauth.StartBrowserAuth(context.Background())
	if err != nil {
		return nil, nil, err
	}
	m.sess = sess
	m.url = sess.URL
	m.State = OAuthStateDisplay

	return m, tea.Batch(m.spinner.Tick, m.waitForToken()), nil
}

func (m *OAuthOpenAI) ID() string { return OAuthOpenAIID }

func (m *OAuthOpenAI) HandleMsg(msg tea.Msg) Action {
	switch msg := msg.(type) {
	case spinner.TickMsg:
		if m.State == OAuthStateInitializing || m.State == OAuthStateDisplay {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			if cmd != nil {
				return ActionCmd{cmd}
			}
		}
	case tea.KeyPressMsg:
		switch {
		case key.Matches(msg, m.keyMap.Copy):
			return ActionCmd{common.CopyToClipboard(m.url, "Link copied to clipboard")}
		case key.Matches(msg, m.keyMap.Submit):
			return ActionCmd{common.CopyToClipboardWithCallback(m.url, "Link copied and URL opened", func() tea.Msg {
				if err := browser.OpenURL(m.url); err != nil {
					return ActionOAuthErrored{Error: fmt.Errorf("failed to open browser: %w", err)}
				}
				return nil
			})}
		case key.Matches(msg, m.keyMap.Close):
			if m.State == OAuthStateSuccess {
				return m.saveKeyAndContinue()
			}
			m.stop()
			return ActionClose{}
		}
	case ActionOAuthErrored:
		m.State = OAuthStateError
		m.stop()
		return ActionCmd{util.ReportError(msg.Error)}
	case ActionCompleteOAuth:
		m.State = OAuthStateSuccess
		m.token = msg.Token
		m.stop()
		return m.saveKeyAndContinue()
	}
	return nil
}

func (m *OAuthOpenAI) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	var (
		t           = m.com.Styles
		dialogStyle = t.Dialog.View.Width(m.width)
	)
	view := dialogStyle.Render(m.dialogContent())
	if m.isOnboarding {
		DrawOnboarding(scr, area, view)
	} else {
		DrawCenter(scr, area, view)
	}
	return nil
}

func (m *OAuthOpenAI) dialogContent() string {
	switch m.State {
	case OAuthStateInitializing:
		return m.innerDialogContent()
	default:
		return strings.Join([]string{m.headerContent(), m.innerDialogContent(), m.help.View(m)}, "\n")
	}
}

func (m *OAuthOpenAI) headerContent() string {
	t := m.com.Styles
	return common.DialogTitle(t, t.Dialog.Title.Render("Let’s authenticate with OpenAI"), m.width, t.Dialog.TitleGradFromColor, t.Dialog.TitleGradToColor)
}

func (m *OAuthOpenAI) innerDialogContent() string {
	t := m.com.Styles
	switch m.State {
	case OAuthStateInitializing:
		return lipgloss.NewStyle().Margin(1, 1).Width(m.width - 2).Align(lipgloss.Center).Render(t.Dialog.OAuth.Success.Render(m.spinner.View()) + t.Dialog.OAuth.StatusText.Render("Initializing..."))
	case OAuthStateDisplay:
		link := t.Dialog.OAuth.Link.Hyperlink(m.url, "id=openai-oauth").Render(m.url)
		return lipgloss.NewStyle().Margin(1, 1).Width(m.width - 2).Render(t.Dialog.OAuth.Instructions.Render("Press ") + t.Dialog.OAuth.Enter.Render("enter") + t.Dialog.OAuth.Instructions.Render(" to copy the link and open your browser.") + "\n\n" + link)
	case OAuthStateSuccess:
		return t.Dialog.OAuth.Success.Margin(1).Width(m.width - 2).Render("Authentication successful!")
	case OAuthStateError:
		return t.Dialog.OAuth.ErrorText.Margin(1).Width(m.width - 2).Render("Authentication failed.")
	default:
		return ""
	}
}

func (m *OAuthOpenAI) FullHelp() [][]key.Binding { return [][]key.Binding{m.ShortHelp()} }

func (m *OAuthOpenAI) ShortHelp() []key.Binding {
	if m.State == OAuthStateSuccess {
		return []key.Binding{key.NewBinding(key.WithKeys("enter", "ctrl+y", "esc"), key.WithHelp("enter", "finish"))}
	}
	return []key.Binding{m.keyMap.Copy, m.keyMap.Submit, m.keyMap.Close}
}

func (m *OAuthOpenAI) waitForToken() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithCancel(context.Background())
		m.cancelFunc = cancel
		token, err := m.sess.Wait(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return ActionOAuthErrored{Error: err}
		}
		return ActionCompleteOAuth{Token: token}
	}
}

func (m *OAuthOpenAI) saveKeyAndContinue() Action {
	err := m.com.Workspace.SetProviderAPIKey(config.ScopeGlobal, string(m.provider.ID), m.token)
	if err != nil {
		return ActionCmd{util.ReportError(fmt.Errorf("failed to save API key: %w", err))}
	}
	return ActionSelectModel{Provider: m.provider, Model: m.model, ModelType: m.modelType}
}

func (m *OAuthOpenAI) stop() {
	if m.cancelFunc != nil {
		m.cancelFunc()
	}
	if m.sess != nil {
		m.sess.Close()
	}
}
