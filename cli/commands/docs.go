package commands

import (
	_ "embed"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/charmbracelet/glamour"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// docsReference is the canonical Orva reference, embedded at compile time. It is
// the same single source (docs/reference.md) the get_orva_docs MCP tool serves;
// `make docs-embed` keeps this copy in sync. {{ORIGIN}} placeholders are
// substituted with the configured instance endpoint so URLs are pasteable.
//
//go:embed reference.md
var docsReference string

const docsPlaceholderOrigin = "https://your-orva-instance.example.com"

var docsCmd = &cobra.Command{
	Use:     "docs",
	GroupID: groupAI,
	Short:   "Show the Orva reference documentation",
	Long: `Render the full Orva reference in the terminal.

This is the same documentation the dashboard and the AI assistant use. On a
terminal it is rendered as styled markdown and paged through $PAGER (or less);
piped or with --raw it prints the raw markdown for grep/redirect.

  orva docs            # rendered + paged
  orva docs --raw      # raw markdown
  orva docs | grep -i webhook`,
	Args: cobra.NoArgs,
	RunE: runDocs,
}

func init() {
	docsCmd.Flags().Bool("raw", false, "print raw markdown without rendering or paging")
}

func runDocs(cmd *cobra.Command, _ []string) error {
	raw, _ := cmd.Flags().GetBool("raw")

	origin := docsPlaceholderOrigin
	if c, err := getClient(cmd); err == nil {
		if o := strings.TrimRight(strings.TrimSpace(c.BaseURL), "/"); o != "" {
			origin = o
		}
	}
	md := strings.ReplaceAll(docsReference, "{{ORIGIN}}", origin)

	if raw || !stdoutIsTerminal() {
		_, err := io.WriteString(os.Stdout, md)
		return err
	}

	width := 100
	if w, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && w > 0 && w < width {
		width = w
	}
	r, err := glamour.NewTermRenderer(glamour.WithAutoStyle(), glamour.WithWordWrap(width))
	if err != nil {
		_, err := io.WriteString(os.Stdout, md)
		return err
	}
	rendered, err := r.Render(md)
	if err != nil {
		_, err := io.WriteString(os.Stdout, md)
		return err
	}
	return pageOrPrint(rendered)
}

// pageOrPrint streams content through the user's pager ($PAGER, else `less -R`)
// when one is available, falling back to a direct write. Best-effort: any
// failure to launch or run the pager degrades to printing the content.
func pageOrPrint(content string) error {
	name, args := "less", []string{"-R"}
	if p := strings.TrimSpace(os.Getenv("PAGER")); p != "" {
		parts := strings.Fields(p)
		name, args = parts[0], parts[1:]
	}
	path, err := exec.LookPath(name)
	if err != nil {
		_, werr := io.WriteString(os.Stdout, content)
		return werr
	}
	c := exec.Command(path, args...)
	c.Stdin = strings.NewReader(content)
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	if err := c.Run(); err != nil {
		_, werr := io.WriteString(os.Stdout, content)
		return werr
	}
	return nil
}
