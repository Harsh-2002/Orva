package workloadasync

import (
	"errors"
	"testing"

	"github.com/Harsh-2002/Orva/backend/internal/database"
)

func TestCronControlRevisionBoundaries(t *testing.T) {
	for _, text := range []string{"1", "9007199254740993", "9223372036854775807"} {
		if _, err := cronRevision(text); err != nil {
			t.Fatalf("exact signed decimal %q rejected: %v", text, err)
		}
	}
	for _, text := range []string{"", "0", "01", "-1", "+1", "1.0", "9.223372036854776e18", "9223372036854775808", "18446744073709551615"} {
		if _, err := cronRevision(text); !errors.Is(err, database.ErrAsyncConflict) {
			t.Fatalf("noncanonical or overflowing %q accepted", text)
		}
	}
}
