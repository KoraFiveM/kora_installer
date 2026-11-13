package main

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"strconv"
	"time"

	"github.com/go-sql-driver/mysql"
)

// Creation only: failures leave new objects intact for inspection. No DROP,
// ALTER, REPLACE, truncation, migrations, or existing-account grants are used.
func createDatabase(ctx context.Context, c Config) error {
	cfg := mysql.NewConfig()
	cfg.User = c.DBAdmin
	cfg.Passwd = c.DBAdminPassword
	cfg.Net = "tcp"
	cfg.Addr = net.JoinHostPort(c.DBHost, strconv.Itoa(c.DBPort))
	cfg.Timeout = 10 * time.Second
	cfg.ReadTimeout = 20 * time.Second
	cfg.WriteTimeout = 20 * time.Second
	cfg.InterpolateParams = true
	db, e := sql.Open("mysql", cfg.FormatDSN())
	if e != nil {
		return errors.New("cannot configure database administrator connection")
	}
	defer db.Close()
	if e = db.PingContext(ctx); e != nil {
		return errors.New("cannot connect to database administrator account; verify host, credentials and permissions")
	}
	return provisionDatabase(ctx, db, c)
}

func provisionDatabase(ctx context.Context, db *sql.DB, c Config) error {
	var e error
	var count int
	if e = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.SCHEMATA WHERE SCHEMA_NAME = ?", c.DBName).Scan(&count); e != nil {
		return errors.New("cannot check database name availability")
	}
	if count != 0 {
		return errors.New("database already exists; choose a new name")
	}
	if e = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM mysql.user WHERE User = ?", c.DBUser).Scan(&count); e != nil {
		return errors.New("cannot check database user availability")
	}
	if count != 0 {
		return errors.New("database account already exists; choose a new user")
	}
	if _, e = db.ExecContext(ctx, "CREATE DATABASE `"+c.DBName+"` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci"); e != nil {
		return errors.New("CREATE DATABASE failed; existing data was not changed")
	}
	if _, e = db.ExecContext(ctx, "CREATE USER ?@'%' IDENTIFIED BY ?", c.DBUser, c.DBPassword); e != nil {
		return errors.New("CREATE USER failed; newly created database retained")
	}
	if _, e = db.ExecContext(ctx, "GRANT ALL PRIVILEGES ON `"+c.DBName+"`.* TO ?@'%'", c.DBUser); e != nil {
		return errors.New("GRANT failed; newly created database and account retained")
	}
	return nil
}

func waitDatabase(ctx context.Context, c Config) error {
	deadline := time.NewTimer(90 * time.Second)
	defer deadline.Stop()
	for {
		conn, e := net.DialTimeout("tcp", net.JoinHostPort(c.DBHost, strconv.Itoa(c.DBPort)), time.Second)
		if e == nil {
			conn.Close()
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return errors.New("database service did not become reachable; inspect its retained data directory and Windows Services")
		case <-time.After(time.Second):
		}
	}
}
