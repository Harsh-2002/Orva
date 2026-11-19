package commands

import (
	"strings"
	"testing"
)

// TestCommandTree proves every advertised subcommand path resolves via
// cobra.Command.Find. A new subcommand that's forgotten in RegisterClient
// will trip this test; same for a subcommand-of-subcommand wiring that
// silently dropped during a refactor.
func TestCommandTree(t *testing.T) {
	root := NewRoot()
	paths := [][]string{
		{"iam"}, {"iam", "policies", "list"}, {"iam", "policies", "get"}, {"iam", "policies", "create"}, {"iam", "policies", "update"}, {"iam", "policies", "impact"}, {"iam", "policies", "attach"}, {"iam", "policies", "detach"}, {"iam", "policies", "revoke"}, {"iam", "policies", "delete"},
		{"iam", "permissions", "get"}, {"iam", "permissions", "put"}, {"iam", "permissions", "review"},
		{"history"}, {"history", "list"}, {"history", "get"}, {"history", "capacity"}, {"history", "purge"}, {"history", "reissue"},
		{"functions"}, {"functions", "list"}, {"functions", "get"}, {"functions", "create"}, {"functions", "delete"}, {"functions", "update"}, {"functions", "purge-cache"},
		{"functions", "pause"}, {"functions", "resume"},
		{"deploy"},
		{"deployments"}, {"deployments", "list"}, {"deployments", "get"}, {"deployments", "logs"}, {"deployments", "install"},
		{"rollback"},
		{"invoke"},
		{"logs"},
		{"executions"}, {"executions", "list"}, {"executions", "get"}, {"executions", "logs"},
		{"executions", "delete"}, {"executions", "prune"}, {"executions", "replay"},
		{"kv"}, {"kv", "get"}, {"kv", "put"}, {"kv", "delete"}, {"kv", "list"}, {"kv", "incr"}, {"kv", "cas"},
		{"cron"}, {"cron", "create"}, {"cron", "list"}, {"cron", "update"}, {"cron", "delete"}, {"cron", "resume"},
		{"jobs"}, {"jobs", "enqueue"}, {"jobs", "list"}, {"jobs", "get"}, {"jobs", "retry"}, {"jobs", "delete"}, {"jobs", "resume"},
		{"secrets"}, {"secrets", "set"}, {"secrets", "list"}, {"secrets", "delete"},
		{"keys"}, {"keys", "list"}, {"keys", "create"}, {"keys", "revoke"},
		{"channels"}, {"channels", "create"}, {"channels", "list"}, {"channels", "show"},
		{"channels", "add-functions"}, {"channels", "remove-functions"}, {"channels", "rotate"}, {"channels", "delete"},
		{"webhooks"}, {"webhooks", "list"}, {"webhooks", "create"}, {"webhooks", "delete"},
		{"webhooks", "test"}, {"webhooks", "deliveries"}, {"webhooks", "retry"},
		{"webhooks", "inbound"}, {"webhooks", "inbound", "list"}, {"webhooks", "inbound", "create"},
		{"webhooks", "inbound", "update"}, {"webhooks", "inbound", "delete"}, {"webhooks", "inbound", "test"},
		{"routes"}, {"routes", "list"}, {"routes", "set"}, {"routes", "delete"},
		{"fixtures"}, {"fixtures", "list"}, {"fixtures", "get"}, {"fixtures", "save"}, {"fixtures", "delete"}, {"fixtures", "test"},
		{"traces"}, {"traces", "list"}, {"traces", "get"}, {"traces", "baseline"},
		{"firewall"}, {"firewall", "list"}, {"firewall", "add"}, {"firewall", "enable"}, {"firewall", "disable"}, {"firewall", "delete"}, {"firewall", "resolve"},
		{"dns"}, {"dns", "get"}, {"dns", "set"},
		{"pool"}, {"pool", "get"}, {"pool", "set"},
		{"system"}, {"system", "health"}, {"system", "metrics"}, {"system", "storage"}, {"system", "db-stats"}, {"system", "vacuum"},
		{"activity"},
		{"backup"}, {"backup", "download"}, {"backup", "restore"},
		{"diff"},
		{"login"},
		{"completion"},
		{"upgrade"},
		{"docs"},
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

func TestChatCommandIsNotRegistered(t *testing.T) {
	root := NewRoot()
	for _, cmd := range root.Commands() {
		if cmd.Name() == "chat" {
			t.Fatal("chat command is still registered")
		}
	}
	root.SetArgs([]string{"chat"})
	if _, err := root.ExecuteC(); err == nil || !strings.Contains(err.Error(), `unknown command "chat"`) {
		t.Fatalf("executing chat returned %v, want an unknown-command error", err)
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
		{[]string{"iam", "policies", "create"}, "request"},
		{[]string{"iam", "policies", "update"}, "request"},
		{[]string{"iam", "policies", "impact"}, "request"},
		{[]string{"iam", "policies", "attach"}, "request"},
		{[]string{"iam", "policies", "detach"}, "request"},
		{[]string{"iam", "policies", "revoke"}, "request"},
		{[]string{"iam", "policies", "delete"}, "request"},
		{[]string{"iam", "permissions", "put"}, "request"},
		{[]string{"iam", "permissions", "review"}, "request"},
		{[]string{"iam", "policies", "get"}, "history"},
		{[]string{"iam", "policies", "get"}, "version"},
		{[]string{"iam", "policies", "list"}, "offset"},
		{[]string{"history", "get"}, "request"},
		{[]string{"history", "purge"}, "request"},
		{[]string{"history", "reissue"}, "request"},
		{[]string{"deploy"}, "name"},
		{[]string{"deploy"}, "runtime"},
		{[]string{"deploy"}, "entrypoint"},
		{[]string{"deploy"}, "watch"},
		{[]string{"deploy"}, "follow"},
		{[]string{"invoke"}, "body"},
		{[]string{"invoke"}, "method"},
		{[]string{"invoke"}, "header"},
		{[]string{"invoke"}, "stream"},
		{[]string{"login"}, "endpoint"},
		{[]string{"login"}, "api-key"},
		{[]string{"logs"}, "follow"},
		{[]string{"logs"}, "exec-id"},
		{[]string{"activity"}, "limit"},
		{[]string{"activity"}, "follow"},
		{[]string{"kv", "list"}, "prefix"},
		{[]string{"kv", "list"}, "limit"},
		{[]string{"kv", "incr"}, "by"},
		{[]string{"kv", "cas"}, "expected"},
		{[]string{"kv", "cas"}, "new"},
		{[]string{"functions", "list"}, "limit"},
		{[]string{"functions", "pause"}, "request"},
		{[]string{"functions", "resume"}, "request"},
		{[]string{"functions", "list"}, "offset"},
		{[]string{"functions", "create"}, "sdk"},
		{[]string{"functions", "update"}, "sdk"},
		{[]string{"traces", "list"}, "before"},
		{[]string{"jobs", "list"}, "status"},
		{[]string{"jobs", "list"}, "fn"},
		{[]string{"upgrade"}, "check"},
		{[]string{"upgrade"}, "force"},
		{[]string{"upgrade"}, "data-dir"},
		{[]string{"cron", "create"}, "fn"},
		{[]string{"cron", "create"}, "expr"},
		{[]string{"cron", "resume"}, "config-revision"},
		{[]string{"cron", "resume"}, "authority-generation"},
		{[]string{"webhooks", "create"}, "name"},
		{[]string{"webhooks", "create"}, "url"},
		{[]string{"webhooks", "inbound", "create"}, "header"},
		{[]string{"webhooks", "inbound", "update"}, "active"},
		{[]string{"webhooks", "inbound", "update"}, "expected-version"},
		{[]string{"webhooks", "inbound", "delete"}, "expected-version"},
		{[]string{"routes", "set"}, "expected-version"},
		{[]string{"routes", "delete"}, "expected-version"},
		{[]string{"webhooks", "update"}, "events"},
		{[]string{"functions", "update"}, "network-mode"},
		{[]string{"functions", "update"}, "env"},
		{[]string{"executions", "list"}, "status"},
		{[]string{"executions", "list"}, "limit"},
		{[]string{"executions", "prune"}, "older-than"},
		{[]string{"channels", "create"}, "functions"},
		{[]string{"secrets", "set"}, "value"},
		{[]string{"keys", "create"}, "expires-in-days"},
		{[]string{"pool", "set"}, "fn"},
		{[]string{"diff"}, "from"},
		{[]string{"diff"}, "to"},
		{[]string{"docs"}, "raw"},
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

// TestPersistentFlags confirms the root persistent flags are present and
// visible to every subcommand. This includes the global output controls that
// the whole CLI's scripting story depends on.
func TestPersistentFlags(t *testing.T) {
	root := NewRoot()
	for _, name := range []string{"endpoint", "api-key", "output", "quiet", "no-color", "yes"} {
		if root.PersistentFlags().Lookup(name) == nil {
			t.Errorf("root missing persistent flag --%s", name)
		}
	}
}

// TestKeysCreateDefaultPermission guards least-privilege: `orva keys create`
// with no --permissions must default to invoke-only, NOT inherit the server's
// all-four default (which would silently mint admin keys for CI/deploy bots).
func TestKeysCreateDefaultPermission(t *testing.T) {
	root := NewRoot()
	cmd, _, err := root.Find([]string{"keys", "create"})
	if err != nil {
		t.Fatalf("find keys create: %v", err)
	}
	f := cmd.Flag("permissions")
	if f == nil {
		t.Fatal("keys create missing --permissions flag")
	}
	if f.DefValue != "invoke" {
		t.Errorf("keys create --permissions default = %q, want \"invoke\"", f.DefValue)
	}
}

// (Exact-asset-name matching is covered by TestUpgradeAssetName in
// upgrade_test.go, which replaced the old regex-filter helper.)

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
