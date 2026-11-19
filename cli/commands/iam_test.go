package commands

import (
	"strings"
	"testing"
)

func TestIAMFlagsHelpAndCompletion(t *testing.T) {
	root := NewRoot()
	for _, op := range []string{"create", "update", "impact", "attach", "detach", "revoke", "delete"} {
		cmd, _, err := root.Find([]string{"iam", "policies", op})
		if err != nil {
			t.Fatal(err)
		}
		flag := cmd.Flags().Lookup("request")
		if flag == nil || !strings.Contains(flag.Usage, "@-") || len(flag.Annotations["cobra_annotation_bash_completion_one_required_flag"]) == 0 {
			t.Fatal("request file/stdin flag not required", op)
		}
		if op != "create" && cmd.ValidArgsFunction == nil {
			t.Fatal("policy-ID completion not installed", op)
		}
		if cmd.RunE == nil {
			t.Fatal("IAM command must return errors", op)
		}
	}
	for _, op := range []string{"get", "put", "review"} {
		cmd, _, err := root.Find([]string{"iam", "permissions", op})
		if err != nil || cmd.ValidArgsFunction == nil {
			t.Fatal("function completion missing", op)
		}
	}
}
