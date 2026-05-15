package commands

import "testing"

// TestCommandTree proves every advertised subcommand path resolves via
// cobra.Command.Find. A new subcommand that's forgotten in RegisterClient
// will trip this test; same for a subcommand-of-subcommand wiring that
// silently dropped during a refactor.
func TestCommandTree(t *testing.T) {
	root := NewRoot()
	paths := [][]string{
		{"functions"}, {"functions", "list"}, {"functions", "get"}, {"functions", "create"}, {"functions", "delete"},
		{"deploy"},
		{"invoke"},
		{"logs"},
		{"kv"}, {"kv", "get"}, {"kv", "put"}, {"kv", "delete"}, {"kv", "list"},
		{"cron"}, {"cron", "create"}, {"cron", "list"}, {"cron", "delete"},
		{"jobs"}, {"jobs", "enqueue"}, {"jobs", "list"}, {"jobs", "retry"}, {"jobs", "delete"},
		{"secrets"}, {"secrets", "set"}, {"secrets", "list"}, {"secrets", "delete"},
		{"keys"}, {"keys", "list"}, {"keys", "create"},
		{"channels"}, {"channels", "create"}, {"channels", "list"}, {"channels", "delete"},
		{"webhooks"}, {"webhooks", "list"}, {"webhooks", "create"}, {"webhooks", "delete"},
		{"routes"}, {"routes", "list"},
		{"system"}, {"system", "health"},
		{"activity"},
		{"backup"}, {"backup", "download"}, {"backup", "restore"},
		{"login"},
		{"completion"},
		{"upgrade"},
	}
	for _, p := range paths {
		cmd, _, err := root.Find(p)
		if err != nil {
			t.Errorf("Find %v: %v", p, err)
			continue
		}
		// cobra.Find may return an ancestor if the leaf doesn't exist —
		// confirm the resolved name matches the final path segment.
		if cmd.Name() != p[len(p)-1] {
			t.Errorf("Find %v: got %q want %q", p, cmd.Name(), p[len(p)-1])
		}
	}
}

// TestRequiredFlagsPresent confirms each subcommand still owns the flags
// downstream tooling (CI scripts, docs examples, the existing test
// harness) relies on. Catches accidental flag drops during refactors.
func TestRequiredFlagsPresent(t *testing.T) {
	cases := []struct {
		path []string
		flag string
	}{
		{[]string{"deploy"}, "name"},
		{[]string{"deploy"}, "runtime"},
		{[]string{"deploy"}, "entrypoint"},
		{[]string{"invoke"}, "data"},
		{[]string{"login"}, "endpoint"},
		{[]string{"login"}, "api-key"},
		{[]string{"logs"}, "tail"},
		{[]string{"logs"}, "exec-id"},
		{[]string{"activity"}, "limit"},
		{[]string{"activity"}, "tail"},
		{[]string{"kv", "list"}, "prefix"},
		{[]string{"kv", "list"}, "limit"},
		{[]string{"jobs", "list"}, "status"},
		{[]string{"jobs", "list"}, "fn"},
		{[]string{"upgrade"}, "check"},
		{[]string{"upgrade"}, "force"},
		{[]string{"cron", "create"}, "fn"},
		{[]string{"cron", "create"}, "expr"},
		{[]string{"webhooks", "create"}, "name"},
		{[]string{"webhooks", "create"}, "url"},
		{[]string{"channels", "create"}, "functions"},
		{[]string{"secrets", "set"}, "value"},
	}
	root := NewRoot()
	for _, c := range cases {
		cmd, _, err := root.Find(c.path)
		if err != nil || cmd == nil {
			t.Errorf("Find %v: %v", c.path, err)
			continue
		}
		if cmd.Flag(c.flag) == nil {
			t.Errorf("%v missing --%s", c.path, c.flag)
		}
	}
}

// TestPersistentFlags confirms the root persistent flags (--endpoint,
// --api-key) are present and visible to every subcommand.
func TestPersistentFlags(t *testing.T) {
	root := NewRoot()
	for _, name := range []string{"endpoint", "api-key"} {
		if root.PersistentFlags().Lookup(name) == nil {
			t.Errorf("root missing persistent flag --%s", name)
		}
	}
}

// TestNewRootSetsVersion confirms the version template is wired up so
// `orva --version` returns the value of commands.Version (set by main()).
func TestNewRootSetsVersion(t *testing.T) {
	prev := Version
	Version = "v9999.12.31"
	t.Cleanup(func() { Version = prev })
	root := NewRoot()
	if root.Version != "v9999.12.31" {
		t.Errorf("root.Version = %q want %q", root.Version, "v9999.12.31")
	}
}
