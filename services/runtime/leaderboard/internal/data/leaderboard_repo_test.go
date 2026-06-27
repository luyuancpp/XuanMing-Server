package data

import (
	"strings"
	"testing"
)

func TestBuildSaveSnapshotSQLQuotesRank(t *testing.T) {
	query, args := buildSaveSnapshotSQL(100, []SnapshotRow{
		{Rank: 1, EntityID: 200, Score: 300, CreatedAtMs: 400},
		{Rank: 2, EntityID: 201, Score: 250, CreatedAtMs: 401},
	})

	if !strings.Contains(query, "`rank`") {
		t.Fatalf("query must quote rank column for MySQL 8: %s", query)
	}
	if strings.Contains(query, " rank,") {
		t.Fatalf("query contains unquoted rank column: %s", query)
	}
	if got, want := len(args), 10; got != want {
		t.Fatalf("args len=%d, want %d", got, want)
	}
}
