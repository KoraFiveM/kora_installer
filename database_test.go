package main

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"strings"
	"testing"
)

type dbScript struct {
	counts     []int64
	statements []string
	args       [][]driver.NamedValue
	fail       int
}
type fakeConnector struct{ s *dbScript }

func (f fakeConnector) Connect(context.Context) (driver.Conn, error) { return &fakeConn{s: f.s}, nil }
func (f fakeConnector) Driver() driver.Driver                        { return fakeDriver{} }

type fakeDriver struct{}

func (fakeDriver) Open(string) (driver.Conn, error) { return nil, errors.New("use connector") }

type fakeConn struct{ s *dbScript }

func (f *fakeConn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("unexpected prepare") }
func (f *fakeConn) Close() error                        { return nil }
func (f *fakeConn) Begin() (driver.Tx, error)           { return nil, errors.New("unexpected transaction") }
func (f *fakeConn) QueryContext(_ context.Context, q string, _ []driver.NamedValue) (driver.Rows, error) {
	if !strings.HasPrefix(q, "SELECT COUNT(*)") {
		return nil, errors.New("unexpected query")
	}
	if len(f.s.counts) == 0 {
		return nil, errors.New("no count result")
	}
	n := f.s.counts[0]
	f.s.counts = f.s.counts[1:]
	return &countRows{n: n}, nil
}
func (f *fakeConn) ExecContext(_ context.Context, q string, args []driver.NamedValue) (driver.Result, error) {
	f.s.statements = append(f.s.statements, q)
	f.s.args = append(f.s.args, args)
	if f.s.fail == len(f.s.statements) {
		return nil, errors.New("simulated failure")
	}
	return driver.RowsAffected(1), nil
}

type countRows struct {
	n    int64
	done bool
}

func (*countRows) Columns() []string { return []string{"count"} }
func (*countRows) Close() error      { return nil }
func (r *countRows) Next(dest []driver.Value) error {
	if r.done {
		return io.EOF
	}
	dest[0] = r.n
	r.done = true
	return nil
}
func TestDatabaseNeverReusesOrDestroys(t *testing.T) {
	for _, tc := range []struct {
		name         string
		counts       []int64
		fail, writes int
		success      bool
	}{{"database exists", []int64{1}, 0, 0, false}, {"user exists", []int64{0, 1}, 0, 0, false}, {"fresh", []int64{0, 0}, 0, 3, true}, {"create user fails", []int64{0, 0}, 2, 2, false}, {"grant fails", []int64{0, 0}, 3, 3, false}} {
		t.Run(tc.name, func(t *testing.T) {
			s := &dbScript{counts: tc.counts, fail: tc.fail}
			db := sql.OpenDB(fakeConnector{s})
			defer db.Close()
			c := validConfig(t)
			e := provisionDatabase(context.Background(), db, c)
			if (e == nil) != tc.success {
				t.Fatalf("unexpected result %v", e)
			}
			if len(s.statements) != tc.writes {
				t.Fatalf("got %d writes, want %d", len(s.statements), tc.writes)
			}
			for _, q := range s.statements {
				for _, forbidden := range []string{"DROP ", "ALTER ", "TRUNCATE ", "IF NOT EXISTS", "REPLACE "} {
					if strings.Contains(q, forbidden) {
						t.Fatalf("destructive query: %s", q)
					}
				}
				if strings.Contains(q, c.DBPassword) {
					t.Fatal("password interpolated into SQL by application")
				}
			}
			if len(s.args) >= 2 && s.args[1][1].Value != c.DBPassword {
				t.Fatal("password did not use driver parameter binding")
			}
		})
	}
}
