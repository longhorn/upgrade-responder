package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"time"
)

type UpgradeChecker struct {
	Address          string
	UpgradeRequester UpgradeRequester
	queryPeriod      time.Duration
	stopCh           chan struct{}
}

type UpgradeRequester interface {
	GetCurrentVersion() string
	GetExtraInfo() map[string]string
	ProcessUpgradeResponse(response *CheckUpgradeResponse, err error)
}

type Version struct {
	Name        string // must be in semantic versioning
	ReleaseDate string
	Tags        []string
}

type CheckUpgradeRequest struct {
	AppVersion string            `json:"appVersion"`
	ExtraInfo  map[string]string `json:"extraInfo"`
}

type CheckUpgradeResponse struct {
	Versions []Version `json:"versions"`
}

func NewUpgradeChecker(address string, upgradeRequester UpgradeRequester) *UpgradeChecker {
	return &UpgradeChecker{
		Address:          address,
		UpgradeRequester: upgradeRequester,
		queryPeriod:      1 * time.Hour,
		stopCh:           make(chan struct{}),
	}
}

func (c *UpgradeChecker) Start() {
	go c.run()
}

func (c *UpgradeChecker) run() {
	ticker := time.NewTicker(c.queryPeriod)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			resp, err := c.CheckUpgrade(c.UpgradeRequester.GetCurrentVersion(), c.UpgradeRequester.GetExtraInfo())
			c.UpgradeRequester.ProcessUpgradeResponse(resp, err)
		case <-c.stopCh:
			return
		}
	}
}

func (c *UpgradeChecker) Stop() {
	select {
	case <-c.stopCh:
		// stopCh channel is already closed
	default:
		close(c.stopCh)
	}
}

func (c *UpgradeChecker) SetQueryPeriod(queryPeriod time.Duration) {
	c.queryPeriod = queryPeriod
}

// CheckUpgrade sends a request that contains the current version of the application and any extra information to the Upgrade Responder server.
// Then it parses and return the response
func (c *UpgradeChecker) CheckUpgrade(currentAppVersion string, extraInfo map[string]string) (*CheckUpgradeResponse, error) {
	var (
		resp    CheckUpgradeResponse
		content bytes.Buffer
	)
	req := &CheckUpgradeRequest{
		AppVersion: currentAppVersion,
		ExtraInfo:  extraInfo,
	}

	if err := json.NewEncoder(&content).Encode(req); err != nil {
		return nil, err
	}

	r, err := http.Post(c.Address, "application/json", &content)
	if err != nil {
		return nil, err
	}
	defer r.Body.Close()
	if r.StatusCode != http.StatusOK {
		message := ""
		messageBytes, err := ioutil.ReadAll(r.Body)
		if err != nil {
			message = err.Error()
		} else {
			message = string(messageBytes)
		}
		return nil, fmt.Errorf("query return status code %v, message %v", r.StatusCode, message)
	}
	if err := json.NewDecoder(r.Body).Decode(&resp); err != nil {
		return nil, err
	}

	return &resp, nil
}
