package main

import (
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Sirupsen/logrus"
	"github.com/pkg/errors"
	"github.com/urfave/cli"

	"github.com/longhorn/upgrade-responder/upgraderesponder"
)

var VERSION = "v0.0.0-dev"

const (
	FlagUpgradeResponseConfiguration = "upgrade-response-config"
	EnvUpgradeResponseConfiguration  = "UPGRADE_RESPONSE_CONFIG"
	FlagRequestSchema                = "request-schema"
	EnvRequestSchema                 = "REQUEST_SCHEMA"
	FlagApplicationName              = "application-name"
	EnvApplicationName               = "APPLICATION_NAME"
	FlagInfluxDBURL                  = "influxdb-url"
	EnvInfluxDBURL                   = "INFLUXDB_URL"
	FlagInfluxDBUser                 = "influxdb-user"
	EnvInfluxDBUser                  = "INFLUXDB_USER"
	FlagInfluxDBPass                 = "influxdb-pass"
	EnvInfluxDBPass                  = "INFLUXDB_PASS"
	FlagQueryPeriod                  = "query-period"
	EnvQueryPeriod                   = "QUERY_PERIOD"
	FlagGeoDB                        = "geodb"
	EnvGeoDB                         = "GEODB"
	FlagPort                         = "port"
	EnvPort                          = "PORT"
	FlagCacheSyncInterval            = "cache-sync-interval"
	EnvCacheSyncInterval             = "CACHE_SYNC_INTERVAL"
	FlagCacheSize                    = "cache-size"
	EnvCacheSize                     = "CACHE_SIZE"
)

func main() {
	app := cli.NewApp()
	app.Name = "upgrade-responder"
	app.Version = VERSION
	app.Flags = []cli.Flag{
		cli.BoolFlag{
			Name:   "debug, d",
			Usage:  "enable debug logging level",
			EnvVar: "DEBUG",
		},
	}
	app.Before = func(c *cli.Context) error {
		if c.GlobalBool("debug") {
			logrus.SetLevel(logrus.DebugLevel)
		}
		return nil
	}

	app.Commands = []cli.Command{
		UpgradeResponderCmd(),
	}

	if err := app.Run(os.Args); err != nil {
		logrus.Fatal(err)
	}
}

func UpgradeResponderCmd() cli.Command {
	return cli.Command{
		Name: "start",
		Flags: []cli.Flag{
			cli.StringFlag{
				Name:   FlagUpgradeResponseConfiguration,
				EnvVar: EnvUpgradeResponseConfiguration,
				Usage:  "Specify the response configuration file for upgrade query",
			},
			cli.StringFlag{
				Name:   FlagRequestSchema,
				EnvVar: EnvRequestSchema,
				Usage:  "Specify the request schema file which contains the rules that the upgrade responder server use to validate request data before writing to database",
			},
			cli.StringFlag{
				Name:   FlagApplicationName,
				EnvVar: EnvApplicationName,
				Usage:  "Specify the name of the application that is using this upgrade checker. This will be used to create a database name <application-name>_upgrade_responder in the InfluxDB to store all data for this upgrade checker",
			},
			cli.StringFlag{
				Name:   FlagInfluxDBURL,
				EnvVar: EnvInfluxDBURL,
				Usage:  "Specify the URL of InfluxDB",
			},
			cli.StringFlag{
				Name:   FlagInfluxDBUser,
				EnvVar: EnvInfluxDBUser,
				Usage:  "Specify the InfluxDB user name",
			},
			cli.StringFlag{
				Name:   FlagInfluxDBPass,
				EnvVar: EnvInfluxDBPass,
				Usage:  "Specify the InfluxDB password",
			},
			cli.StringFlag{
				Name:   FlagQueryPeriod,
				EnvVar: EnvQueryPeriod,
				Value:  "1h",
				Usage:  "Specify the period for how often each instance of the application makes the request. Cannot change after set for the first time. This value should be the same as time in GROUP BY clause in Grafana",
			},
			cli.StringFlag{
				Name:   FlagGeoDB,
				EnvVar: EnvGeoDB,
				Usage:  "Specify the path of to GeoDB file",
			},
			cli.IntFlag{
				Name:   FlagPort,
				EnvVar: EnvPort,
				Value:  8314,
				Usage:  "Specify the port number",
			},
			cli.IntFlag{
				Name:   FlagCacheSyncInterval,
				EnvVar: EnvCacheSyncInterval,
				Value:  1,
				Usage:  "Specify the period for how often the server should sync data to database. Measured in second. The server aggregates the client requests before writing to influxDB.",
			},
			cli.IntFlag{
				Name:   FlagCacheSize,
				EnvVar: EnvCacheSize,
				Value:  100,
				Usage:  "Specify the cache size of server. Once the number of data points in cache is bigger than cache size, the server flush and write all data in the cache to influxDB.",
			},
		},
		Action: func(c *cli.Context) error {
			return startUpgradeResponder(c)
		},
	}
}

func startUpgradeResponder(c *cli.Context) error {
	if err := validateCommandLineArguments(c); err != nil {
		return err
	}

	responseConfigFile := c.String(FlagUpgradeResponseConfiguration)
	requestSchemaFile := c.String(FlagRequestSchema)
	influxURL := c.String(FlagInfluxDBURL)
	influxUser := c.String(FlagInfluxDBUser)
	influxPass := c.String(FlagInfluxDBPass)
	queryPeriod := c.String(FlagQueryPeriod)
	applicationName := c.String(FlagApplicationName)
	geodb := c.String(FlagGeoDB)
	port := c.Int(FlagPort)
	cacheSyncInterval := c.Int(FlagCacheSyncInterval)
	cacheSize := c.Int(FlagCacheSize)

	done := make(chan struct{})
	server, err := upgraderesponder.NewServer(done, applicationName, responseConfigFile, requestSchemaFile, influxURL, influxUser, influxPass, queryPeriod, geodb, cacheSyncInterval, cacheSize)
	if err != nil {
		return err
	}
	router := http.Handler(upgraderesponder.NewRouter(server))

	listeningAddress := fmt.Sprintf("0.0.0.0:%v", port)

	go func() {
		logrus.Infof("Server is listening at %v", listeningAddress)
		// always returns error. ErrServerClosed on graceful close
		if err := http.ListenAndServe(listeningAddress, router); err != http.ErrServerClosed {
			logrus.Fatalf("%v", err)
		}
		<-done
	}()

	RegisterShutdownChannel(done)
	<-done
	return nil
}

func RegisterShutdownChannel(done chan struct{}) {
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigs
		logrus.Infof("Receive %v to exit", sig)
		close(done)
	}()
}

func validateCommandLineArguments(c *cli.Context) error {
	responseConfigFile := c.String(FlagUpgradeResponseConfiguration)
	if responseConfigFile == "" {
		return fmt.Errorf("no upgrade response configuration file specified")
	}

	requestSchemaFile := c.String(FlagRequestSchema)
	if requestSchemaFile == "" {
		return fmt.Errorf("no request schema file specified")
	}

	applicationName := c.String(FlagApplicationName)
	if applicationName == "" {
		return fmt.Errorf("no application name specified")
	}

	queryPeriod := c.String(FlagQueryPeriod)
	if _, err := time.ParseDuration(queryPeriod); err != nil {
		return errors.Wrap(err, "fail to parse --query-period")
	}

	return nil
}
