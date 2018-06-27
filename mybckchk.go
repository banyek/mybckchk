package main

import (
	"database/sql"
	"flag"
	"fmt"
	_ "github.com/go-sql-driver/mysql"
	"github.com/koding/logging"
	"gopkg.in/ini.v1"
	"net/http"
	"os"
	"time"
)

var stateCache, debug bool
var logger = logging.NewLogger("MySQL backend checker")

type configuration struct {
	mysqlHost     string
	mysqlUser     string
	mysqlPass     string
	mysqlPort     int
	mysqlDb       string
	listenPort    int
	checkInterval int
}
type command struct {
	query  string
	expect string
}

func baseHandler(w http.ResponseWriter, r *http.Request) {
	if stateCache {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	} else {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("NOT OK"))
	}
}

func main() {
	configfile := flag.String("cfg", "mybckchk.cfg", "Configuration file")
	debugEnabled := flag.Bool("debug", false, "Debug messages")
	alwaysEnabled := flag.Bool("enable", false, "Return always enabled")
	alwaysDisabled := flag.Bool("disable", false, "Return always disabled")
	flag.Parse()
	debug = false
	stateCache = false
	if *debugEnabled {
		debug = true
		logger.Notice("Debug mode enabled")
	}
	config, commands := configure(*configfile)
	logger.Notice("Configuration loaded")
	if *alwaysEnabled && *alwaysDisabled {
		logger.Error("-enable and -disable can't used both.")
		os.Exit(1)
	} else if *alwaysEnabled {
		logger.Notice("Always reporting available backend")
		stateCache = true
	} else if *alwaysDisabled {
		logger.Notice("Always reporting unavailable backend")
	} else {
		go checkController(config, commands)
	}
	http.HandleFunc("/", baseHandler)
	listenOn := fmt.Sprint(":", config.listenPort)
	http.ListenAndServe(listenOn, nil)
}
func debugLog(msg string) {
	if debug {
		logger.Info(msg)
	}
}
func checkController(config *configuration, commands []command) {
	tick := time.NewTicker(time.Millisecond * time.Duration(config.checkInterval)).C
	for {
		select {
		case <-tick:
			checkBackend(config, commands)
		}
	}
}
func checkBackend(config *configuration, commands []command) {
	var result string
	preCheckState := stateCache
	debugLog("Running checks")
	checkOK := true
	for _, command := range commands {
		connecturi := mysqlURIBuilder(config)
		db, err := sql.Open("mysql", connecturi)
		defer db.Close()
		debugLog(command.query)
		err = db.Ping()
		if err != nil {
			logger.Error(err.Error())
		}
		sqlRes, err := db.Prepare(command.query)
		if err != nil {
			logger.Error(err.Error())
		} else {
			err = sqlRes.QueryRow().Scan(&result)
		}
		if err != nil {
			logger.Error(err.Error())
		}
		debugLog(result)
		debugLog(command.expect)
		if result != command.expect {
			checkOK = false
		}
	}
	stateCache = checkOK
	if preCheckState != stateCache {
		if stateCache {
			logger.Notice("Backend state changed: Backend enabled")
		} else {
			logger.Notice("Backend state changed: Backend disabled")
		}
	}

}
func mysqlURIBuilder(config *configuration) string {
	uri := ""
	if config.mysqlHost == "" { // if mysqlHost is not defined, we'll connect through local socket
		uri = fmt.Sprint(config.mysqlUser, ":", config.mysqlPass, "@", "/", config.mysqlDb)
	} else { // if we use TCP we'll also need the port of mysql too
		uri = fmt.Sprint(config.mysqlUser, ":", config.mysqlPass, "@tcp(", config.mysqlHost, ":", config.mysqlPort, ")/", config.mysqlDb)
	}
	debugLog(uri)
	return uri
}

func connectDb(config *configuration) *sql.DB {
	connectUri := mysqlURIBuilder(config)
	db, err := sql.Open("mysql", connectUri)
	if err != nil {
		logger.Error(err.Error())
	}
	err = db.Ping()
	if err != nil {
		logger.Error(err.Error())
	}
	debugLog("Connected to database")
	return db
}
func configure(cfgfile string) (*configuration, []command) {
	var mysqlPort, listenPort, checkInterval int
	var conf configuration
	var commands []command
	config, err := ini.Load(cfgfile)
	if err != nil {
		logger.Error("Can't load config file!")
		logger.Critical(err.Error())
		os.Exit(1)
	}
	sections := config.Sections()
	for _, section := range sections {
		if section.Name() != "DEFAULT" { // skip unnamed section
			if section.Name() == "config" { // [config] holds the configuratuin
				mysqlPort, err = section.Key("mysql_port").Int()
				if mysqlPort == 0 {
					mysqlPort = 3306
				}
				listenPort, err = section.Key("listen").Int()
				if listenPort == 0 {
					listenPort = 9200
				}
				checkInterval, err = section.Key("check_interval").Int()
				if checkInterval == 0 {
					checkInterval = 1000
				}
				conf = configuration{
					mysqlHost:     section.Key("mysql_host").String(),
					mysqlUser:     section.Key("mysql_user").String(),
					mysqlPass:     section.Key("mysql_password").String(),
					mysqlPort:     mysqlPort,
					mysqlDb:       section.Key("mysql_db").String(),
					listenPort:    listenPort,
					checkInterval: checkInterval,
				}
				debugLog("Config loaded")
			} else { // here start the command parsing
				var cmd command
				cmd = command{
					query:  section.Key("query").String(),
					expect: section.Key("expect").String(),
				}
				commands = append(commands, cmd)
			}
		}
	}
	return &conf, commands
}
