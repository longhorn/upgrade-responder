package main

import (
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/Sirupsen/logrus"
	"github.com/urfave/cli"

	"github.com/longhorn/upgrade-responder/upgraderesponder"
)

var VERSION = "v0.0.0-dev"

const (
	FlagUpgradeResponseConfiguration = "upgrade-response-config"
	EnvUpgradeResponseConfiguration  = "UPGRADE_RESPONSE_CONFIG"
	FlagInfluxDBURL                  = "influxdb-url"
	EnvInfluxDBURL                   = "INFLUXDB_URL"
	FlagInfluxDBUser                 = "influxdb-user"
	EnvInfluxDBUser                  = "INFLUXDB_USER"
	FlagInfluxDBPass                 = "influxdb-pass"
	EnvInfluxDBPass                  = "INFLUXDB_PASS"
	FlagGeoDB                        = "geodb"
	EnvGeoDB                         = "GEODB"
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
				Usage:  "Specify the response configuration for upgrade query",
			},
			cli.StringFlag{
				Name:   FlagInfluxDBURL,
				EnvVar: EnvInfluxDBURL,
				Usage:  "Specify the location of InfluxDB",
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
				Name:   FlagGeoDB,
				EnvVar: EnvGeoDB,
				Usage:  "Specify the location of GeoDB",
			},
		},
		Action: func(c *cli.Context) error {
			return startUpgradeResponder(c)
		},
	}
}

func startUpgradeResponder(c *cli.Context) error {
	cfg := c.String(FlagUpgradeResponseConfiguration)
	if cfg == "" {
		return fmt.Errorf("no upgrade response configuration specified")
	}

	influxURL := c.String(FlagInfluxDBURL)
	influxUser := c.String(FlagInfluxDBUser)
	influxPass := c.String(FlagInfluxDBPass)
	geodb := c.String(FlagGeoDB)
	done := make(chan struct{})
	server, err := upgraderesponder.NewServer(done, cfg, influxURL, influxUser, influxPass, geodb)
	if err != nil {
		return err
	}
	router := http.Handler(upgraderesponder.NewRouter(server))

	listen := "0.0.0.0:8314"
	go http.ListenAndServe(listen, router)

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
