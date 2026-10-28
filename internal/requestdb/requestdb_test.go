package requestdb

import (
	"context"
	"database/sql"
	"encoding/csv"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/api"
)

// TestLedgerUsageAndBodies 验证关闭前排队的记录落盘、当前与上一周期分开汇总、正文只保留最近条数
func TestLedgerUsageAndBodies(t *testing.T) {
	path := filepath.Join(t.TempDir(), "requests.db")
	logs := func(level, message string) { t.Errorf("%s %s", level, message) }
	store, err := Open(path, logs)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	store.Record(Row{ID: "a", Time: now.Add(-time.Hour), Model: "flash", RequestedModel: "alias", ResponseModel: "response-model",
		State: "completed", Duration: 2 * time.Second, InputTokens: 10, CachedTokens: 4, TotalTokens: 30})
	store.Record(Row{ID: "b", Time: now.Add(-2 * time.Hour), Model: "flash", State: "failed", Status: 429, Duration: 4 * time.Second})
	store.Record(Row{ID: "c", Time: now.Add(-3 * time.Hour), Model: "pro", State: "completed", Duration: 6 * time.Second, TotalTokens: 5})
	store.Record(Row{ID: "old", Time: now.Add(-30 * time.Hour), Model: "pro", State: "completed", Duration: time.Second})
	for index := range bodyRetention + 2 {
		store.SaveBody(api.RequestBody{ID: fmt.Sprintf("body-%d", index), Time: now, Request: "in", Response: "out"})
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(path, logs)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	report, err := store.Usage(context.Background(), api.UsageQuery{
		From: now.Add(-24 * time.Hour), To: now, Bucket: time.Hour, Location: time.UTC, Stack: "model",
	})
	if err != nil {
		t.Fatal(err)
	}
	totals := report.Totals
	if totals.Requests != 3 || totals.Failed != 1 || totals.RateLimited != 1 || totals.TotalTokens != 35 || report.Previous.Requests != 1 {
		t.Fatalf("totals=%+v previous=%+v", totals, report.Previous)
	}
	if len(report.Buckets) != 25 || math.Abs(totals.Duration.P95MS-6000) > 60 || totals.Duration.AvgMS != 4000 {
		t.Fatalf("buckets=%d duration=%+v", len(report.Buckets), totals.Duration)
	}
	bucketed := int64(0)
	for _, bucket := range report.Buckets {
		bucketed += bucket.Requests
	}
	models := report.Groups["model"]
	if bucketed != 3 || len(models) != 2 || models[0].Key != "flash" || models[0].Requests != 2 {
		t.Fatalf("bucketed=%d models=%+v", bucketed, models)
	}
	page, err := store.Records(context.Background(), api.UsageRecordQuery{
		From: now.Add(-24 * time.Hour), To: now, Filters: api.UsageFilters{"model": {"flash"}}, Limit: 10,
	})
	if err != nil || len(page.Items) != 2 {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	if record := page.Items[0]; record.ID != "a" || record.Model != "flash" || record.RequestedModel != "alias" || record.ResponseModel != "response-model" || record.CachedTokens != 4 {
		t.Fatalf("record=%+v", record)
	}
	if record := page.Items[1]; record.ID != "b" || record.Model != "flash" || record.RequestedModel != "" || record.ResponseModel != "" {
		t.Fatalf("record=%+v", record)
	}
	if _, err := store.RequestBody(context.Background(), "body-1"); !errors.Is(err, api.ErrRequestBodyNotFound) {
		t.Fatalf("超出保留条数的正文仍存在: %v", err)
	}
	body, err := store.RequestBody(context.Background(), fmt.Sprintf("body-%d", bodyRetention+1))
	if err != nil || body.Request != "in" || body.Response != "out" {
		t.Fatalf("body=%+v err=%v", body, err)
	}
}

func TestLedgerModelFieldsMigration(t *testing.T) {
	const legacySchema = `CREATE TABLE requests (
 id TEXT PRIMARY KEY, time INTEGER NOT NULL, protocol TEXT NOT NULL, path TEXT NOT NULL,
 model TEXT NOT NULL, account TEXT NOT NULL, channel TEXT NOT NULL, status INTEGER NOT NULL, state TEXT NOT NULL,
 duration_ms INTEGER NOT NULL, first_event_ms INTEGER NOT NULL, queue_ms INTEGER NOT NULL,
 input_tokens INTEGER NOT NULL, reasoning_tokens INTEGER NOT NULL, reply_tokens INTEGER NOT NULL,
 total_tokens INTEGER NOT NULL, tool_calls INTEGER NOT NULL, error TEXT NOT NULL, attempts TEXT NOT NULL)`
	for _, tc := range []struct {
		name, columns string
		cached        bool
	}{
		{"legacy", "", false}, {"requested_only", "requested_model", false}, {"response_only", "response_model", false},
		{"fork", "requested_model,response_model", false}, {"upstream", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "requests.db")
			db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if _, err := db.Exec(legacySchema); err != nil {
				t.Fatal(err)
			}
			for _, column := range strings.FieldsFunc(tc.columns, func(r rune) bool { return r == ',' }) {
				if _, err := db.Exec(`ALTER TABLE requests ADD COLUMN ` + column + ` TEXT NOT NULL DEFAULT ''`); err != nil {
					t.Fatal(err)
				}
			}
			if tc.cached {
				if _, err := db.Exec(`ALTER TABLE requests ADD COLUMN cached_tokens INTEGER NOT NULL DEFAULT 0`); err != nil {
					t.Fatal(err)
				}
			}
			for _, table := range []string{"hours", "days"} {
				oldColumns := strings.Replace(rollupColumns, ", cached_tokens INTEGER NOT NULL", "", 1)
				if _, err := db.Exec(`CREATE TABLE ` + table + ` (` + oldColumns + `) WITHOUT ROWID`); err != nil {
					t.Fatal(err)
				}
			}
			now := time.Now().UTC().Truncate(time.Millisecond)
			if _, err := db.Exec(`INSERT INTO requests (
 id, time, protocol, path, model, account, channel, status, state,
 duration_ms, first_event_ms, queue_ms, input_tokens, reasoning_tokens, reply_tokens, total_tokens, tool_calls, error, attempts)
 VALUES ('old',?,'openai-chat','/v1/chat/completions','legacy-model','account','build',200,'completed',100,20,0,0,0,0,0,0,'','[]')`, now.UnixMilli()); err != nil {
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			logs := func(level, message string) { t.Errorf("%s %s", level, message) }
			store, err := Open(path, logs)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			store.Record(Row{ID: "new", Time: now.Add(time.Millisecond), Model: "resolved-model",
				RequestedModel: "requested-alias", ResponseModel: "response-json-model", CachedTokens: 3, State: "completed"})
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			store, err = Open(path, logs)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			page, err := store.Records(context.Background(), api.UsageRecordQuery{From: now, To: now.Add(time.Second), Limit: 10})
			if err != nil || len(page.Items) != 2 {
				t.Fatalf("page=%+v err=%v", page, err)
			}
			if record := page.Items[0]; record.ID != "new" || record.Model != "resolved-model" || record.RequestedModel != "requested-alias" || record.ResponseModel != "response-json-model" || record.CachedTokens != 3 {
				t.Fatalf("new record=%+v", record)
			}
			if record := page.Items[1]; record.ID != "old" || record.Model != "legacy-model" || record.RequestedModel != "" || record.ResponseModel != "" || record.CachedTokens != 0 {
				t.Fatalf("old record=%+v", record)
			}
		})
	}
}

func TestExportRecordsModelColumns(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "requests.db"), func(level, message string) { t.Errorf("%s %s", level, message) })
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC()
	if err := store.write([]write{
		{row: &Row{ID: "new", Time: now.Add(time.Millisecond), Model: "resolved-model", RequestedModel: "=requested", ResponseModel: "+response"}},
		{row: &Row{ID: "old", Time: now, Model: "legacy-model"}},
	}); err != nil {
		t.Fatal(err)
	}
	var output strings.Builder
	if err := store.ExportRecords(context.Background(), api.UsageRecordQuery{From: now.Add(-time.Second), To: now.Add(time.Second)}, &output); err != nil {
		t.Fatal(err)
	}
	records, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(output.String(), "\xef\xbb\xbf"))).ReadAll()
	if err != nil || len(records) != 3 {
		t.Fatalf("records=%v err=%v", records, err)
	}
	wantHeader := []string{
		"time", "id", "protocol", "path", "model", "account", "channel", "status", "state", "duration_ms", "first_event_ms",
		"queue_ms", "input_tokens", "cached_tokens", "reasoning_tokens", "reply_tokens", "total_tokens", "tool_calls", "error", "attempts",
		"requested_model", "response_model",
	}
	if !slices.Equal(records[0], wantHeader) {
		t.Fatalf("header=%v want=%v", records[0], wantHeader)
	}
	if record := records[1]; record[1] != "new" || record[4] != "resolved-model" || record[13] != "0" || record[19] != "[]" || record[20] != "'=requested" || record[21] != "'+response" {
		t.Fatalf("new record=%v", record)
	}
	if record := records[2]; record[1] != "old" || record[4] != "legacy-model" || record[13] != "0" || record[19] != "[]" || record[20] != "" || record[21] != "" {
		t.Fatalf("old record=%v", record)
	}
}
