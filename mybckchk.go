package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/go-sql-driver/mysql"
	"gopkg.in/ini.v1"
)

type configuration struct {
	mysql    *mysql.Config
	listenOn string
	interval time.Duration
	timeout  time.Duration
}

type command struct {
	query  string
	expect string
}

type healthState struct {
	mu         sync.RWMutex
	validUntil time.Time
}

func (h *healthState) publish(validUntil time.Time) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.validUntil = validUntil
}

func (h *healthState) healthy() bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return time.Now().Before(h.validUntil)
}

func healthHandler(healthy func() bool) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		if healthy() {
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, "OK")
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, "NOT OK")
	}
}

func main() {
	configFile := flag.String("cfg", "mybckchk.cfg", "Configuration file")
	debug := flag.Bool("debug", false, "Debug messages")
	enable := flag.Bool("enable", false, "Always report available")
	disable := flag.Bool("disable", false, "Always report unavailable")
	flag.Parse()

	if *enable && *disable {
		log.Fatal("-enable and -disable cannot be used together")
	}
	if err := run(*configFile, *enable, *disable, *debug); err != nil {
		log.Fatal(err)
	}
}

func run(configFile string, enable, disable, debug bool) error {
	override := enable || disable
	config, commands, err := configure(configFile, override)
	if err != nil {
		return err
	}

	healthy := func() bool { return enable }
	if !override {
		connector, err := mysql.NewConnector(config.mysql)
		if err != nil {
			return errors.New("invalid MySQL connection configuration")
		}
		db := sql.OpenDB(connector)
		db.SetMaxOpenConns(1)
		defer db.Close()

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		state := &healthState{}
		healthy = state.healthy
		go checkController(ctx, db, config, commands, state, debug)
	}

	server := &http.Server{
		Addr:              config.listenOn,
		Handler:           healthHandler(healthy),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       5 * time.Second,
		WriteTimeout:      5 * time.Second,
		IdleTimeout:       30 * time.Second,
	}
	return server.ListenAndServe()
}

func checkController(ctx context.Context, db *sql.DB, config configuration, commands []command, state *healthState, debug bool) {
	ticker := time.NewTicker(config.interval)
	defer ticker.Stop()

	previous := false
	for ctx.Err() == nil {
		checkCtx, cancel := context.WithTimeout(ctx, config.timeout)
		err := checkBackend(checkCtx, db, commands)
		cancel()

		healthy := err == nil
		if healthy {
			state.publish(time.Now().Add(config.interval + config.timeout))
		} else {
			state.publish(time.Time{})
			if debug {
				log.Printf("Backend check failed: %v", err)
			}
		}
		if healthy != previous {
			log.Printf("Backend health changed: available=%t", healthy)
			previous = healthy
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func checkBackend(ctx context.Context, db *sql.DB, commands []command) error {
	if len(commands) == 0 {
		return errors.New("no checks configured")
	}
	for i, command := range commands {
		var result string
		if err := db.QueryRowContext(ctx, command.query).Scan(&result); err != nil {
			if ctx.Err() != nil {
				return fmt.Errorf("check %d: %w", i+1, ctx.Err())
			}
			return fmt.Errorf("check %d: query failed or returned an invalid result", i+1)
		}
		if result != command.expect {
			return fmt.Errorf("check %d: unexpected result", i+1)
		}
	}
	return ctx.Err()
}

func configure(path string, override bool) (configuration, []command, error) {
	var config configuration
	file, err := ini.Load(path)
	if err != nil {
		return config, nil, fmt.Errorf("cannot read or parse configuration file %q", path)
	}
	settings, err := file.GetSection("config")
	if err != nil {
		return config, nil, errors.New("missing [config] section")
	}

	listenPort, err := intSetting(settings, "listen", 9200, 65535)
	if err != nil {
		return config, nil, err
	}
	config.listenOn = net.JoinHostPort(settings.Key("listen_host").String(), strconv.Itoa(listenPort))
	if override {
		return config, nil, nil
	}

	const maxCheckMillis = 24 * 60 * 60 * 1000
	interval, err := intSetting(settings, "check_interval", 1000, maxCheckMillis)
	if err != nil {
		return config, nil, err
	}
	timeout, err := intSetting(settings, "check_timeout", interval, maxCheckMillis)
	if err != nil {
		return config, nil, err
	}
	config.interval = time.Duration(interval) * time.Millisecond
	config.timeout = time.Duration(timeout) * time.Millisecond

	mysqlConfig := mysql.NewConfig()
	mysqlConfig.User = settings.Key("mysql_user").String()
	mysqlConfig.Passwd = settings.Key("mysql_password").String()
	mysqlConfig.DBName = settings.Key("mysql_db").String()
	mysqlConfig.Timeout = config.timeout
	mysqlConfig.ReadTimeout = config.timeout
	mysqlConfig.WriteTimeout = config.timeout

	if socket := settings.Key("mysql_socket").String(); socket != "" {
		if settings.Key("mysql_host").String() != "" || settings.HasKey("mysql_tls") {
			return config, nil, errors.New("mysql_socket cannot be combined with mysql_host or mysql_tls")
		}
		mysqlConfig.Net = "unix"
		mysqlConfig.Addr = socket
	} else {
		host := settings.Key("mysql_host").String()
		if host == "" {
			host = "localhost"
		}
		port, err := intSetting(settings, "mysql_port", 3306, 65535)
		if err != nil {
			return config, nil, err
		}
		mysqlConfig.Net = "tcp"
		mysqlConfig.Addr = net.JoinHostPort(host, strconv.Itoa(port))
		tlsEnabled := host != "localhost" && !net.ParseIP(host).IsLoopback()
		if settings.HasKey("mysql_tls") {
			tlsEnabled, err = strconv.ParseBool(settings.Key("mysql_tls").String())
			if err != nil {
				return config, nil, errors.New("mysql_tls must be true or false")
			}
		}
		if tlsEnabled {
			mysqlConfig.TLSConfig = "true"
		}
	}
	config.mysql = mysqlConfig

	var commands []command
	for _, section := range file.Sections() {
		switch section.Name() {
		case "DEFAULT", "config":
			continue
		}
		query := section.Key("query").String()
		if query == "" || !section.HasKey("expect") {
			return config, nil, fmt.Errorf("check %d requires query and expect", len(commands)+1)
		}
		commands = append(commands, command{query: query, expect: section.Key("expect").String()})
	}
	if len(commands) == 0 {
		return config, nil, errors.New("at least one check must be configured")
	}
	return config, commands, nil
}

func intSetting(section *ini.Section, name string, fallback, max int) (int, error) {
	if !section.HasKey(name) {
		return fallback, nil
	}
	value, err := strconv.Atoi(section.Key(name).String())
	if err == nil && value == 0 {
		return fallback, nil
	}
	if err != nil || value < 1 || value > max {
		return 0, fmt.Errorf("%s must be 0 (default) or an integer from 1 to %d", name, max)
	}
	return value, nil
}
