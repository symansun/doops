package server

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"errors"
	"net"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/go-sql-driver/mysql"
)

// gatewayMySQLConfig is used by offline migration tooling. It does not select
// an alternate store for the running Gateway. Never format or log this value.
type gatewayMySQLConfig struct {
	Host     string
	Port     uint16
	User     string
	Password string
	Database string
	Roots    *x509.CertPool
}

// The driver can include server-supplied text in diagnostic messages. Only
// explicitly categorized errors cross the boundary to the operator.
type gatewayMySQLQuietLogger struct{}

func (gatewayMySQLQuietLogger) Print(...any) {}

func (c gatewayMySQLConfig) driverConfig() (*mysql.Config, error) {
	if c.Host == "" || strings.ContainsAny(c.Host, "/@?#[]%") || strings.ContainsFunc(c.Host, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) ||
		(strings.Contains(c.Host, ":") && net.ParseIP(c.Host) == nil) {
		return nil, errors.New("mysql_host_invalid")
	}
	if c.User == "" || strings.ContainsRune(c.User, 0) {
		return nil, errors.New("mysql_user_invalid")
	}
	if c.Password == "" || strings.ContainsRune(c.Password, 0) {
		return nil, errors.New("mysql_secret_invalid")
	}
	// Keep the destination explicit and unambiguous; no DSN or session
	// parameters can be smuggled into the schema field.
	if len(c.Database) == 0 || len(c.Database) > 64 || strings.ContainsFunc(c.Database, func(r rune) bool {
		return !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_')
	}) {
		return nil, errors.New("mysql_database_invalid")
	}
	switch c.Database {
	case "mysql", "information_schema", "performance_schema", "sys":
		return nil, errors.New("mysql_database_reserved")
	}
	port := c.Port
	if port == 0 {
		port = 3306
	}
	cfg := mysql.NewConfig()
	cfg.Net = "tcp"
	cfg.Addr = net.JoinHostPort(c.Host, strconv.Itoa(int(port)))
	cfg.User = c.User
	cfg.Passwd = c.Password
	cfg.DBName = c.Database
	cfg.Timeout = timeouts.MySQLConnectTimeout
	cfg.TLS = &tls.Config{ServerName: c.Host, RootCAs: c.Roots, MinVersion: tls.VersionTLS12}
	cfg.Logger = gatewayMySQLQuietLogger{}
	cfg.Collation = "utf8mb4_0900_bin"
	cfg.RejectReadOnly = true
	return cfg, nil
}

func openGatewayMySQL(ctx context.Context, config gatewayMySQLConfig) (*sql.DB, error) {
	cfg, err := config.driverConfig()
	if err != nil {
		return nil, err
	}
	connector, err := mysql.NewConnector(cfg)
	if err != nil {
		return nil, errors.New("mysql_connector_failed")
	}
	db := sql.OpenDB(connector)
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(4)
	db.SetConnMaxIdleTime(2 * time.Minute)
	pingCtx, cancel := context.WithTimeout(ctx, mysqlPingTimeout())
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		db.Close()
		return nil, gatewayMySQLConnectionError(err)
	}
	return db, nil
}

func gatewayMySQLConnectionError(err error) error {
	var serverError *mysql.MySQLError
	var unknownCA x509.UnknownAuthorityError
	var badHostname x509.HostnameError
	var invalidCertificate x509.CertificateInvalidError
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return errors.New("mysql_connect_timeout")
	case errors.Is(err, context.Canceled):
		return errors.New("mysql_connect_cancelled")
	case errors.Is(err, mysql.ErrNoTLS), errors.As(err, &unknownCA), errors.As(err, &badHostname), errors.As(err, &invalidCertificate):
		return errors.New("mysql_tls_verification_failed")
	case errors.As(err, &serverError):
		switch serverError.Number {
		case 1044:
			return errors.New("mysql_database_access_denied")
		case 1045:
			return errors.New("mysql_authentication_failed")
		case 1049:
			return errors.New("mysql_database_not_found")
		}
	}
	return errors.New("mysql_connect_failed")
}
