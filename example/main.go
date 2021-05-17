package main

import (
	"github.com/Sirupsen/logrus"
	"os"
	"os/signal"
	"syscall"

	"github.com/longhorn/upgrade-responder/client"
)

const (
	// example.com should be replaced by your Upgrade Responder server address
	upgradeResponderServerAddress = "https://example.com/v1/checkupgrade"

	version           = "v0.8.1"
	VersionTagLatest  = "latest"
	kubernetesVersion = "v1.19.1"
)

func main() {
	done := make(chan struct{})

	upgradeChecker := client.NewUpgradeChecker(upgradeResponderServerAddress, &MyUpgradeRequester{})
	upgradeChecker.Start()
	defer upgradeChecker.Stop()

	registerShutdownChannel(done)
	<-done
}

type MyUpgradeRequester struct{}

func (f *MyUpgradeRequester) GetCurrentVersion() string {
	return version
}

func (f *MyUpgradeRequester) GetExtraInfo() map[string]string {
	return map[string]string{"kubernetesVersion": kubernetesVersion}
}

func (f *MyUpgradeRequester) ProcessUpgradeResponse(resp *client.CheckUpgradeResponse, err error) {
	if err != nil {
		logrus.WithError(err).Error("failed to check upgrade")
		return
	}
	latestVersion := ""
	for _, v := range resp.Versions {
		found := false
		for _, tag := range v.Tags {
			if tag == VersionTagLatest {
				found = true
				break
			}
		}
		if found {
			latestVersion = v.Name
			break
		}
	}
	if latestVersion == "" {
		logrus.Errorf("cannot find latest version in response: %+v", resp)
	} else {
		logrus.Infof("The latest version is %v", latestVersion)
	}
}

func registerShutdownChannel(done chan struct{}) {
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigs
		logrus.Infof("Receive %v to exit", sig)
		close(done)
	}()
}
