package database

import "testing"

func TestManagedDefinitionAndMembershipCASAreIndependent(t *testing.T) {
	for _, first := range []string{"update", "attach"} {
		t.Run(first, func(t *testing.T) {
			db, ref, owners := managedDB(t, 1)
			p := managedApply(t, db, ref, IAMManagedMutation{Operation: "create", Name: "reader", PolicyJSON: []byte(managedReader)})
			update := managedChange(p, "update", nil)
			update.PolicyJSON = []byte(managedEmpty)
			attach := managedChange(p, "attach", managedExpected(t, db, owners))
			up, err := db.PrepareIAMManagedMutation(t.Context(), ref, update)
			if err != nil {
				t.Fatal(err)
			}
			at, err := db.PrepareIAMManagedMutation(t.Context(), ref, attach)
			if err != nil {
				t.Fatal(err)
			}
			before, after := up, at
			if first == "attach" {
				before, after = at, up
			}
			if err := db.CommitIAMManagedMutation(t.Context(), ref, before); err != nil {
				t.Fatal(err)
			}
			if err := db.CommitIAMManagedMutation(t.Context(), ref, after); err != nil {
				t.Fatal("unrelated CAS domain conflicted", err)
			}
			got, err := db.GetIAMManagedPolicy(t.Context(), p.ID)
			if err != nil || got.DefinitionRevision != 2 || got.MembershipRevision != 2 || got.LatestVersion != 2 {
				t.Fatal("concurrent counter overwritten", got, err)
			}
			if *after.Policy() != *got {
				t.Fatal("acknowledgement carried stale unowned counter")
			}
			s, err := db.GetIAMSources(t.Context(), owners[0])
			if err != nil || s.Attachments[0].Version != 1 || s.Record.PolicyJSON != managedReader {
				t.Fatal("independent version creation changed explicit pin", err)
			}
		})
	}
}
