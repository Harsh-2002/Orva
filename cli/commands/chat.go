package commands

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"

	"github.com/Harsh-2002/Orva/cli/commands/theme"
	cli "github.com/Harsh-2002/Orva/internal/client"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// chatCmd is the terminal entry point to the Orva AI assistant — the same
// agent the dashboard's AI sidebar drives, reached over the AI SSE backend
// with the CLI's existing API key. It runs as an interactive streaming REPL,
// or one-shot with -p for inline/piped use. All AI configuration (providers,
// keys, approval policy) is done in the web UI; the CLI reads and respects it.
var chatCmd = &cobra.Command{
	Use:     "chat [message]",
	GroupID: groupAI,
	Short:   "Chat with the Orva AI assistant",
	Long: `Talk to the Orva AI assistant from the terminal.

Run with no arguments for an interactive streaming REPL, or pass -p (or a
positional message) for a one-shot reply that prints to stdout and exits —
pipe-friendly for scripting.

The assistant can operate the instance end-to-end (list/deploy functions,
read logs, manage secrets, …). Write/destructive tools pause for your [y/N]
approval per the server's approval policy; reads and invokes run freely.

Providers, API keys, the default model, and the approval policy are configured
in the web UI under Settings → AI. The CLI uses the saved selection; override
just for this session with --provider/--model/--thinking.

Examples:
  orva chat                          # interactive REPL
  orva chat -p "list my functions"   # one-shot, prints to stdout
  echo "what failed today?" | orva chat -p @-
  orva chat --model gpt-4o -p "summarize recent errors"

REPL slash commands: /help /model /thinking /new /clear /yolo /exit`,
	Args: cobra.ArbitraryArgs,
	RunE: runChat,
}

func init() {
	f := chatCmd.Flags()
	f.StringP("prompt", "p", "", "one-shot prompt (inline text, @file, or @- for stdin); non-interactive")
	f.String("provider", "", "provider override for this session (default: saved selection)")
	f.String("model", "", "model override for this session (default: saved selection)")
	f.String("thinking", "", "reasoning effort: off | standard | deep")
	f.String("conversation", "", "resume an existing conversation by id")
	f.Bool("auto-approve", false, "auto-approve tool calls that would otherwise require confirmation (use with care)")
	f.Bool("raw", false, "stream plain text; skip markdown (glamour) rendering")

	// Static completion for the thinking enum; --model is completed dynamically
	// (see completions.go) once a provider is known.
	_ = chatCmd.RegisterFlagCompletionFunc("thinking", fixedCompletion("off", "standard", "deep"))
}

// ─── DTOs (mirror the backend AI JSON shapes) ───────────────────────────────

type aiSettings struct {
	Provider         string            `json:"provider"`
	Model            string            `json:"model"`
	ThinkingLevel    string            `json:"thinking_level"`
	ApprovalPolicy   string            `json:"approval_policy"`
	ActiveProviderID string            `json:"active_provider_id"`
	ProviderModels   map[string]string `json:"provider_models"`
}

type providerView struct {
	ID       string `json:"id"`
	Provider string `json:"provider"`
	Label    string `json:"label"`
	HasKey   bool   `json:"has_key"`
	Enabled  bool   `json:"enabled"`
}

type modelInfo struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// ─── session ────────────────────────────────────────────────────────────────

type chatSession struct {
	cmd    *cobra.Command
	client *cli.Client
	styles *theme.Styles
	md     *glamour.TermRenderer // nil when not rendering markdown
	stdin  *bufio.Reader
	out    io.Writer // data / assistant text (defaults to os.Stdout)
	errOut io.Writer // status / chrome (defaults to os.Stderr)

	convID      string
	provider    string
	model       string
	thinking    string
	autoApprove bool
	raw         bool
	interactive bool // REPL mode (vs one-shot)

	settings  *aiSettings
	providers []providerView
	toolNames map[string]string // tool_call id → tool name (for result lines)
}

// pendingTool is a tool call awaiting the operator's approve/reject decision.
type pendingTool struct {
	ID   string
	Name string
	Args json.RawMessage
}

// turnResult captures the terminal state of one SSE stream (chat or an
// approve/reject continuation).
type turnResult struct {
	pending  []pendingTool
	awaiting bool
	done     bool
	note     string
	errMsg   string
}

func newChatSession(cmd *cobra.Command, client *cli.Client) *chatSession {
	provider, _ := cmd.Flags().GetString("provider")
	model, _ := cmd.Flags().GetString("model")
	thinking, _ := cmd.Flags().GetString("thinking")
	conv, _ := cmd.Flags().GetString("conversation")
	auto, _ := cmd.Flags().GetBool("auto-approve")
	raw, _ := cmd.Flags().GetBool("raw")

	s := &chatSession{
		cmd:         cmd,
		client:      client,
		styles:      styles(cmd),
		stdin:       bufio.NewReader(os.Stdin),
		out:         os.Stdout,
		errOut:      os.Stderr,
		convID:      conv,
		provider:    provider,
		model:       model,
		thinking:    thinking,
		autoApprove: auto,
		raw:         raw,
		toolNames:   map[string]string{},
	}
	s.initRenderer()
	return s
}

// initRenderer builds the glamour markdown renderer for TTY stdout. It stays
// nil (plain streaming) when stdout isn't a terminal, when --raw is set, or
// when ORVA_CHAT_NO_GLAMOUR is set — the scripting/escape-hatch paths.
func (s *chatSession) initRenderer() {
	if s.raw || !stdoutIsTerminal() || os.Getenv("ORVA_CHAT_NO_GLAMOUR") != "" {
		return
	}
	w := s.termWidth()
	if w <= 0 {
		w = 80
	}
	if w > 120 {
		w = 120
	}
	r, err := glamour.NewTermRenderer(glamour.WithAutoStyle(), glamour.WithWordWrap(w))
	if err == nil {
		s.md = r
	}
}

func (s *chatSession) renderMarkdown() bool { return s.md != nil }

func (s *chatSession) termWidth() int {
	w, _, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		return 0
	}
	return w
}

func (s *chatSession) termSize() (int, int) {
	w, h, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		return 0, 0
	}
	return w, h
}

// ─── command entry ──────────────────────────────────────────────────────────

func runChat(cmd *cobra.Command, args []string) error {
	if t, _ := cmd.Flags().GetString("thinking"); t != "" && t != "off" && t != "standard" && t != "deep" {
		return fmt.Errorf("invalid --thinking %q (want off | standard | deep)", t)
	}

	// Resolve the one-shot prompt: -p (supports @file / @-) wins, else any
	// positional args are joined into the prompt.
	prompt := ""
	if p, _ := cmd.Flags().GetString("prompt"); p != "" {
		b, err := readBodyArg(p)
		if err != nil {
			return fmt.Errorf("read prompt: %w", err)
		}
		prompt = strings.TrimSpace(string(b))
	} else if len(args) > 0 {
		prompt = strings.TrimSpace(strings.Join(args, " "))
	}

	client, err := getClient(cmd)
	if err != nil {
		return err
	}
	s := newChatSession(cmd, client)
	if err := s.ensureProvider(); err != nil {
		return err
	}

	if prompt != "" {
		return s.runTurn(cmd.Context(), prompt)
	}

	if !term.IsTerminal(int(os.Stdin.Fd())) || !stdoutIsTerminal() {
		return errors.New("interactive chat needs a terminal; use `orva chat -p \"...\"` for one-shot or piped use")
	}
	s.interactive = true
	return s.repl(cmd.Context())
}

// ensureProvider verifies at least one usable provider is configured (so we
// fail with a clear message rather than an opaque SSE error) and caches the
// provider list + settings for the banner and pickers.
func (s *chatSession) ensureProvider() error {
	provs, err := s.fetchProviders()
	if err != nil {
		return fmt.Errorf("load AI providers: %w", err)
	}
	usable := 0
	for _, p := range provs {
		if p.Enabled && p.HasKey {
			usable++
		}
	}
	if usable == 0 {
		return errors.New("no AI provider configured — add one in the web UI under Settings → AI, then retry")
	}
	s.providers = provs
	if st, err := s.fetchSettings(); err == nil {
		s.settings = st
	}
	return nil
}

// ─── REPL ───────────────────────────────────────────────────────────────────

func (s *chatSession) repl(parent context.Context) error {
	s.printBanner()
	for {
		fmt.Fprint(s.errOut, s.styles.Prompt.Render("you ▸ "))
		line, err := s.stdin.ReadString('\n')
		if err != nil { // Ctrl-D / EOF
			fmt.Fprintln(s.errOut)
			fmt.Fprintln(s.errOut, s.styles.Muted.Render("bye"))
			return nil
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "/") {
			if s.handleSlash(parent, line) {
				return nil
			}
			continue
		}
		if err := s.runTurn(parent, line); err != nil && !errors.Is(err, context.Canceled) {
			s.printError(err.Error())
		}
	}
}

func (s *chatSession) printBanner() {
	provider, model, thinking, policy := s.activeProvider(), s.activeModel(), s.activeThinking(), s.activePolicy()
	head := s.styles.Banner.Render("Orva AI")
	meta := s.styles.Muted.Render(fmt.Sprintf("%s · %s · thinking:%s · approval:%s", provider, model, thinking, policy))
	fmt.Fprintf(s.errOut, "\n  %s  %s\n", head, meta)
	fmt.Fprintln(s.errOut, s.styles.Muted.Render("  Type a message, /help for commands, Ctrl-D to exit.\n"))
}

func (s *chatSession) activeProvider() string {
	if s.provider != "" {
		return s.provider
	}
	if s.settings != nil && s.settings.Provider != "" {
		return s.settings.Provider
	}
	return "(default)"
}

func (s *chatSession) activeModel() string {
	if s.model != "" {
		return s.model
	}
	if s.settings != nil && s.settings.Model != "" {
		return s.settings.Model
	}
	return "(default)"
}

func (s *chatSession) activeThinking() string {
	if s.thinking != "" {
		return s.thinking
	}
	if s.settings != nil && s.settings.ThinkingLevel != "" {
		return s.settings.ThinkingLevel
	}
	return "standard"
}

func (s *chatSession) activePolicy() string {
	if s.settings != nil && s.settings.ApprovalPolicy != "" {
		return s.settings.ApprovalPolicy
	}
	return "all_writes"
}

// handleSlash runs a /command. It returns true to exit the REPL.
func (s *chatSession) handleSlash(parent context.Context, line string) bool {
	fields := strings.Fields(line)
	cmd := fields[0]
	arg := strings.TrimSpace(strings.TrimPrefix(line, cmd))
	switch cmd {
	case "/exit", "/quit":
		return true
	case "/help":
		s.printHelp()
	case "/new":
		s.convID = ""
		fmt.Fprintln(s.errOut, s.styles.Muted.Render("started a new conversation"))
	case "/clear":
		if stdoutIsTerminal() {
			fmt.Fprint(s.out, "\x1b[2J\x1b[H")
		}
	case "/yolo":
		s.autoApprove = !s.autoApprove
		if s.autoApprove {
			fmt.Fprintln(s.errOut, s.styles.Warn.Render("⚠ auto-approve ON — tool calls run without confirmation"))
		} else {
			fmt.Fprintln(s.errOut, s.styles.Muted.Render("auto-approve off"))
		}
	case "/thinking":
		s.setThinking(arg)
	case "/model":
		if err := s.pickModel(); err != nil {
			s.printError(err.Error())
		}
	default:
		fmt.Fprintln(s.errOut, s.styles.Muted.Render("unknown command — try /help"))
	}
	return false
}

func (s *chatSession) printHelp() {
	lines := []string{
		"/help       show this help",
		"/model      choose provider + model (persists)",
		"/thinking   set reasoning effort: off | standard | deep",
		"/new        start a fresh conversation",
		"/clear      clear the screen",
		"/yolo       toggle auto-approve for tool calls",
		"/exit       leave the chat",
	}
	fmt.Fprintln(s.errOut)
	for _, l := range lines {
		fmt.Fprintln(s.errOut, "  "+s.styles.Muted.Render(l))
	}
	fmt.Fprintln(s.errOut)
}

func (s *chatSession) setThinking(arg string) {
	level := strings.TrimSpace(arg)
	if level == "" {
		fmt.Fprintln(s.errOut, s.styles.Muted.Render("usage: /thinking off | standard | deep"))
		return
	}
	if level != "off" && level != "standard" && level != "deep" {
		s.printError(fmt.Sprintf("invalid level %q (want off | standard | deep)", level))
		return
	}
	s.thinking = level
	if err := s.putSelection("", "", "", level); err != nil {
		s.printError(err.Error())
		return
	}
	fmt.Fprintln(s.errOut, s.styles.Muted.Render("thinking set to "+level))
}

// ─── turn execution + streaming ─────────────────────────────────────────────

func (s *chatSession) runTurn(parent context.Context, content string) error {
	ctx, cancel := s.turnContext(parent)
	defer cancel()

	if s.interactive {
		fmt.Fprintln(s.errOut, s.styles.Banner.Render("orva ▸"))
	}

	resp, err := s.postChat(ctx, content)
	if err != nil {
		return s.classify(err)
	}
	res, err := s.drive(resp)
	if err != nil {
		return s.classify(err)
	}
	if res.awaiting && len(res.pending) > 0 {
		res, err = s.handleApprovals(ctx, res.pending)
		if err != nil {
			return s.classify(err)
		}
	}
	if res.note != "" {
		fmt.Fprintln(s.errOut, s.styles.Muted.Render("("+res.note+")"))
	}
	if res.errMsg != "" {
		// Return without printing: the REPL prints returned errors itself and
		// the one-shot path surfaces them via cobra — printing here too showed
		// every stream error twice.
		return errors.New(res.errMsg)
	}
	return nil
}

// turnContext returns a context that is cancelled on Ctrl-C for the duration
// of one turn (chat stream + any approval continuations). Cancelling aborts the
// in-flight request rather than killing the process; an idle Ctrl-C at the
// prompt uses the default behavior (exit) because the handler is detached when
// the turn ends.
func (s *chatSession) turnContext(parent context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	go func() {
		select {
		case <-sigCh:
			cancel()
		case <-ctx.Done():
		}
		signal.Stop(sigCh)
	}()
	return ctx, cancel
}

func (s *chatSession) classify(err error) error {
	if errors.Is(err, context.Canceled) {
		fmt.Fprintln(s.errOut, s.styles.Muted.Render("\n(interrupted)"))
		return err
	}
	return err
}

func (s *chatSession) postChat(ctx context.Context, content string) (*http.Response, error) {
	body := map[string]string{"content": content}
	if s.convID != "" {
		body["conversation_id"] = s.convID
	}
	if s.provider != "" {
		body["provider"] = s.provider
	}
	if s.model != "" {
		body["model"] = s.model
	}
	if s.thinking != "" {
		body["thinking"] = s.thinking
	}
	j, _ := json.Marshal(body)
	return s.client.Send(cli.Request{
		Method:      http.MethodPost,
		Path:        "/api/v1/ai/chat",
		Accept:      "text/event-stream",
		ContentType: "application/json",
		NoTimeout:   true,
		Ctx:         ctx,
		Body:        bytes.NewReader(j),
	})
}

// drive consumes one SSE stream, rendering events as they arrive, and returns
// its terminal state. Shared by the initial chat POST and the approve/reject
// continuations (which are themselves SSE streams resuming the same turn).
func (s *chatSession) drive(resp *http.Response) (turnResult, error) {
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return turnResult{}, checkResponse(resp) // reads + closes the body
	}
	defer resp.Body.Close()

	var res turnResult
	var text strings.Builder
	textStarted := false
	interleaved := false
	seenContent := false // suppress leading whitespace some models emit after thinking

	err := consumeSSE(resp, func(event, data string) (bool, error) {
		switch event {
		case "conversation":
			var d struct {
				ID string `json:"id"`
			}
			_ = json.Unmarshal([]byte(data), &d)
			if d.ID != "" {
				s.convID = d.ID
			}
		case "message_start":
			text.Reset()
			textStarted = false
			interleaved = false
			seenContent = false
		case "delta":
			var d struct {
				Text string `json:"text"`
			}
			_ = json.Unmarshal([]byte(data), &d)
			txt := d.Text
			if !seenContent {
				txt = strings.TrimLeft(txt, " \t\r\n")
				if txt == "" {
					return false, nil
				}
				seenContent = true
			}
			fmt.Fprint(s.out, txt)
			text.WriteString(txt)
			textStarted = true
		case "thinking":
			if !isQuiet(s.cmd) {
				var d struct {
					Text string `json:"text"`
				}
				_ = json.Unmarshal([]byte(data), &d)
				fmt.Fprint(s.errOut, s.styles.Muted.Render(d.Text))
				if textStarted {
					interleaved = true
				}
			}
		case "tool_call":
			var d struct {
				ID               string          `json:"id"`
				Name             string          `json:"name"`
				Args             json.RawMessage `json:"args"`
				RequiresApproval bool            `json:"requires_approval"`
			}
			_ = json.Unmarshal([]byte(data), &d)
			s.toolNames[d.ID] = d.Name
			if d.RequiresApproval {
				res.pending = append(res.pending, pendingTool{ID: d.ID, Name: d.Name, Args: d.Args})
			} else {
				s.printToolLine(d.Name, "running…", toolRun)
			}
		case "tool_result":
			var d struct {
				ID     string `json:"id"`
				Status string `json:"status"`
			}
			_ = json.Unmarshal([]byte(data), &d)
			name := s.toolNames[d.ID]
			if name == "" {
				name = "tool"
			}
			switch d.Status {
			case "succeeded":
				s.printToolLine(name, "✓", toolOK)
			case "failed":
				s.printToolLine(name, "✗ failed", toolFail)
			case "rejected":
				s.printToolLine(name, "✗ rejected", toolFail)
			default:
				s.printToolLine(name, d.Status, toolRun)
			}
		case "message_end":
			s.finishMessage(text.String(), textStarted && !interleaved)
		case "awaiting_approval":
			res.awaiting = true
			return true, nil
		case "done":
			var d struct {
				Note string `json:"note"`
			}
			_ = json.Unmarshal([]byte(data), &d)
			res.done = true
			res.note = d.Note
			return true, nil
		case "error":
			var d struct {
				Message string `json:"message"`
			}
			_ = json.Unmarshal([]byte(data), &d)
			res.errMsg = d.Message
			// The error path sends no message_end — close out whatever
			// streamed so the error doesn't land on the same row as a
			// truncated half-answer.
			s.finishMessage(text.String(), textStarted && !interleaved)
			return true, nil
		}
		return false, nil
	})
	return res, err
}

// finishMessage closes out one assistant message. In plain mode it just ends
// the streamed line. In TTY markdown mode — when the streamed block is safe to
// reprint (contiguous, fits the viewport) — it erases the raw stream and
// reprints it rendered with glamour. If anything is uncertain it leaves the raw
// text in place (still readable), never corrupting the screen.
func (s *chatSession) finishMessage(text string, safe bool) {
	// A message with no text (e.g. one that only carried tool calls) leaves
	// nothing on stdout — don't emit a stray blank line.
	if strings.TrimSpace(text) == "" {
		return
	}
	if !s.renderMarkdown() {
		fmt.Fprintln(s.out)
		return
	}
	rendered, err := s.md.Render(text)
	if err != nil {
		fmt.Fprintln(s.out)
		return
	}
	w, h := s.termSize()
	rows := screenRows(text, w)
	if !safe || w <= 0 || h <= 0 || rows >= h {
		fmt.Fprintln(s.out)
		return
	}
	// Move to the top of the streamed block (cursor is on its last row) and
	// clear to end of screen, then print the rendered version.
	fmt.Fprintf(s.out, "\r\x1b[%dA\x1b[0J", rows-1)
	fmt.Fprint(s.out, rendered)
}

// screenRows counts the terminal rows a string occupies, accounting for soft
// wrapping at the given width (display width via lipgloss, so wide runes and
// any ANSI are measured correctly).
func screenRows(s string, width int) int {
	if width <= 0 {
		width = 80
	}
	rows := 0
	for _, line := range strings.Split(s, "\n") {
		w := lipgloss.Width(line)
		if w == 0 {
			rows++
			continue
		}
		rows += (w + width - 1) / width
	}
	if rows == 0 {
		rows = 1
	}
	return rows
}

// ─── tool approval ──────────────────────────────────────────────────────────

func (s *chatSession) handleApprovals(ctx context.Context, pending []pendingTool) (turnResult, error) {
	work := append([]pendingTool(nil), pending...)
	var last turnResult
	for i := 0; i < len(work); i++ {
		tc := work[i]
		approve, err := s.decideApproval(tc)
		if err != nil {
			return last, err
		}
		verb := "reject"
		if approve {
			verb = "approve"
		}
		path := "/api/v1/ai/tool-calls/" + url.PathEscape(tc.ID) + "/" + verb
		resp, err := s.client.Send(cli.Request{
			Method: http.MethodPost, Path: path, Accept: "text/event-stream", NoTimeout: true, Ctx: ctx,
		})
		if err != nil {
			return last, err
		}
		res, err := s.drive(resp)
		if err != nil {
			return res, err
		}
		last = res
		if res.errMsg != "" {
			return res, nil
		}
		// A continuation may surface fresh gated calls; fold them into the queue.
		work = append(work, res.pending...)
		if res.done {
			return res, nil
		}
	}
	return last, nil
}

func (s *chatSession) decideApproval(tc pendingTool) (bool, error) {
	if s.autoApprove {
		s.printToolLine(tc.Name, "auto-approved", toolWarn)
		return true, nil
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return false, fmt.Errorf("tool %q requires approval; re-run in an interactive terminal or pass --auto-approve", tc.Name)
	}
	summary := compactArgs(tc.Args)
	fmt.Fprintf(s.errOut, "%s %s %s [y/N] ",
		s.styles.Warn.Render("⚙ approve"), s.styles.Banner.Render(tc.Name), s.styles.Muted.Render(summary))
	line, _ := s.stdin.ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true, nil
	default:
		return false, nil
	}
}

func compactArgs(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return ""
	}
	b, _ := json.Marshal(v)
	str := string(b)
	if str == "{}" || str == "null" {
		return ""
	}
	if len(str) > 120 {
		str = str[:117] + "..."
	}
	return str
}

// ─── tool status lines ──────────────────────────────────────────────────────

type toolKind int

const (
	toolRun toolKind = iota
	toolOK
	toolFail
	toolWarn
)

func (s *chatSession) printToolLine(name, status string, kind toolKind) {
	if isQuiet(s.cmd) {
		return
	}
	var st string
	switch kind {
	case toolOK:
		st = s.styles.Success.Render(status)
	case toolFail:
		st = s.styles.Error.Render(status)
	case toolWarn:
		st = s.styles.Warn.Render(status)
	default:
		st = s.styles.Muted.Render(status)
	}
	fmt.Fprintf(s.errOut, "%s %s %s\n", s.styles.Muted.Render("⚙"), name, st)
}

func (s *chatSession) printError(msg string) {
	fmt.Fprintln(s.errOut, s.styles.Error.Render("error: "+msg))
}

// ─── model / provider picker ────────────────────────────────────────────────

func (s *chatSession) pickModel() error {
	provs, err := s.fetchProviders()
	if err != nil {
		return err
	}
	usable := provs[:0]
	for _, p := range provs {
		if p.Enabled && p.HasKey {
			usable = append(usable, p)
		}
	}
	if len(usable) == 0 {
		return errors.New("no usable provider configured")
	}
	fmt.Fprintln(s.errOut)
	for i, p := range usable {
		label := p.Label
		if label == "" {
			label = p.Provider
		}
		fmt.Fprintf(s.errOut, "  %s %s %s\n",
			s.styles.Primary.Render(fmt.Sprintf("%d)", i+1)), label, s.styles.Muted.Render("("+p.Provider+")"))
	}
	prov, ok := s.pickIndex("provider", len(usable))
	if !ok {
		return nil
	}
	chosen := usable[prov]

	models, listErr, err := s.fetchModels(chosen.ID)
	if err != nil {
		return err
	}
	var model string
	if listErr != "" || len(models) == 0 {
		if listErr != "" {
			fmt.Fprintln(s.errOut, s.styles.Muted.Render("could not list models ("+listErr+")"))
		}
		fmt.Fprint(s.errOut, s.styles.Prompt.Render("model id ▸ "))
		line, _ := s.stdin.ReadString('\n')
		model = strings.TrimSpace(line)
		if model == "" {
			return nil
		}
	} else {
		fmt.Fprintln(s.errOut)
		for i, m := range models {
			label := m.Label
			if label == "" {
				label = m.ID
			}
			fmt.Fprintf(s.errOut, "  %s %s\n", s.styles.Primary.Render(fmt.Sprintf("%d)", i+1)), label)
		}
		mi, ok := s.pickIndex("model", len(models))
		if !ok {
			return nil
		}
		model = models[mi].ID
	}

	if err := s.putSelection(chosen.ID, chosen.Provider, model, ""); err != nil {
		return err
	}
	s.provider = chosen.Provider
	s.model = model
	if s.settings != nil {
		s.settings.Provider = chosen.Provider
		s.settings.Model = model
		s.settings.ActiveProviderID = chosen.ID
	}
	fmt.Fprintln(s.errOut, s.styles.Muted.Render("using "+chosen.Provider+" / "+model))
	return nil
}

func (s *chatSession) pickIndex(label string, n int) (int, bool) {
	fmt.Fprint(s.errOut, s.styles.Prompt.Render(fmt.Sprintf("%s number ▸ ", label)))
	line, _ := s.stdin.ReadString('\n')
	line = strings.TrimSpace(line)
	if line == "" {
		return 0, false
	}
	var idx int
	if _, err := fmt.Sscanf(line, "%d", &idx); err != nil || idx < 1 || idx > n {
		s.printError("invalid selection")
		return 0, false
	}
	return idx - 1, true
}

// ─── AI API helpers ─────────────────────────────────────────────────────────

func (s *chatSession) fetchSettings() (*aiSettings, error) {
	resp, err := s.client.Get("/api/v1/ai/settings")
	if err != nil {
		return nil, err
	}
	if err := checkResponse(resp); err != nil {
		return nil, err
	}
	var out struct {
		Settings aiSettings `json:"settings"`
	}
	if err := decodeJSON(resp, &out); err != nil {
		return nil, err
	}
	return &out.Settings, nil
}

func (s *chatSession) fetchProviders() ([]providerView, error) {
	resp, err := s.client.Get("/api/v1/ai/providers")
	if err != nil {
		return nil, err
	}
	if err := checkResponse(resp); err != nil {
		return nil, err
	}
	var out struct {
		Providers []providerView `json:"providers"`
	}
	if err := decodeJSON(resp, &out); err != nil {
		return nil, err
	}
	return out.Providers, nil
}

func (s *chatSession) fetchModels(providerID string) ([]modelInfo, string, error) {
	resp, err := s.client.Get("/api/v1/ai/providers/" + url.PathEscape(providerID) + "/models")
	if err != nil {
		return nil, "", err
	}
	if err := checkResponse(resp); err != nil {
		return nil, "", err
	}
	var out struct {
		Models []modelInfo `json:"models"`
		Error  string      `json:"error"`
	}
	if err := decodeJSON(resp, &out); err != nil {
		return nil, "", err
	}
	return out.Models, out.Error, nil
}

func (s *chatSession) putSelection(providerID, provider, model, thinking string) error {
	body := map[string]string{}
	if providerID != "" {
		body["provider_id"] = providerID
	}
	if provider != "" {
		body["provider"] = provider
	}
	if model != "" {
		body["model"] = model
	}
	if thinking != "" {
		body["thinking"] = thinking
	}
	resp, err := s.client.Put("/api/v1/ai/selection", body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return checkResponse(resp)
}
