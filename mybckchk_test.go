package main

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type checkDriver struct{}

func (checkDriver) Open(string) (driver.Conn, error) { return checkConn{}, nil }

type checkConn struct{}

func (checkConn) Close() error { return nil }
func (checkConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("prepare unsupported")
}
func (checkConn) Begin() (driver.Tx, error) {
	return nil, errors.New("transactions unsupported")
}
func (checkConn) QueryContext(ctx context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	switch query {
	case "error":
		return nil, errors.New("query failed")
	case "wait":
		<-ctx.Done()
		return nil, ctx.Err()
	case "empty":
		return &checkRows{done: true}, nil
	default:
		return &checkRows{value: query}, nil
	}
}

type checkRows struct {
	value string
	done  bool
}

func (*checkRows) Columns() []string { return []string{"value"} }
func (*checkRows) Close() error      { return nil }
func (r *checkRows) Next(values []driver.Value) error {
	if r.done {
		return io.EOF
	}
	values[0] = r.value
	r.done = true
	return nil
}

func init() {
	sql.Register("mybckchk-test", checkDriver{})
}

func TestCheckBackend(t *testing.T) {
	db, err := sql.Open("mybckchk-test", "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	tests := []struct {
		name     string
		commands []command
		wantErr  bool
	}{
		{"matching results", []command{{"1", "1"}, {"2", "2"}}, false},
		{"mismatch", []command{{"1", "2"}}, true},
		{"no checks", nil, true},
		{"failure after matching result", []command{{"1", "1"}, {"error", "1"}}, true},
		{"failure with empty expectation", []command{{"error", ""}}, true},
		{"no rows", []command{{"empty", ""}}, true},
		{"deadline", []command{{"wait", ""}}, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer cancel()
			err := checkBackend(ctx, db, test.commands)
			if (err != nil) != test.wantErr {
				t.Fatalf("checkBackend error = %v, want error %t", err, test.wantErr)
			}
			if test.name == "deadline" && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("expected deadline error, got %v", err)
			}
		})
	}
}

func TestHealthHandler(t *testing.T) {
	state := &healthState{}
	handler := healthHandler(state.healthy)
	for _, test := range []struct {
		name       string
		validUntil time.Time
		status     int
		body       string
	}{
		{"no result", time.Time{}, 500, "NOT OK"},
		{"fresh", time.Now().Add(time.Minute), 200, "OK"},
		{"stale", time.Now().Add(-time.Minute), 500, "NOT OK"},
	} {
		t.Run(test.name, func(t *testing.T) {
			state.publish(test.validUntil)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest("GET", "/", nil))
			if response.Code != test.status || response.Body.String() != test.body {
				t.Fatalf("got %d %q, want %d %q", response.Code, response.Body, test.status, test.body)
			}
		})
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 100; i++ {
			state.publish(time.Now().Add(time.Minute))
		}
	}()
	for i := 0; i < 100; i++ {
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	}
	<-done
}

func TestConfigure(t *testing.T) {
	for _, test := range []struct {
		name     string
		settings string
		override bool
		wantErr  bool
	}{
		{"defaults", "[config]\n[check]\nquery=SELECT 1\nexpect=1\n", false, false},
		{"missing config", "[check]\nquery=SELECT 1\nexpect=1\n", false, true},
		{"invalid listener", "[config]\nlisten=65536\n", true, true},
		{"negative interval", "[config]\ncheck_interval=-1\n", false, true},
		{"missing checks", "[config]\n", false, true},
		{"missing expectation", "[config]\n[check]\nquery=SELECT 1\n", false, true},
		{"empty expectation", "[config]\n[check]\nquery=SELECT ''\nexpect=\n", false, false},
		{"override without checks", "[config]\n", true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "mybckchk.cfg")
			if err := os.WriteFile(path, []byte(test.settings), 0600); err != nil {
				t.Fatal(err)
			}
			if _, _, err := configure(path, test.override); (err != nil) != test.wantErr {
				t.Fatalf("configure error = %v, want error %t", err, test.wantErr)
			}
		})
	}
}
