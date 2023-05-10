package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type UpgradeChecker struct {
	Address                string
	UpgradeRequester       UpgradeRequester
	DefaultRequestInterval time.Duration
	stopCh                 chan struct{}
}

type UpgradeRequester interface {
	GetCurrentVersion() string
	GetExtraInfo() map[string]string
	ProcessUpgradeResponse(response *CheckUpgradeResponse, err error)
}

type Version struct {
	Name                 string            `json:"name"` // must be in semantic versioning
	ReleaseDate          string            `json:"releaseDate"`
	MinUpgradableVersion string            `json:"minUpgradableVersion"`
	Tags                 []string          `json:"tags"`
	ExtraInfo            map[string]string `json:"extraInfo"`
}

type CheckUpgradeRequest struct {
	AppVersion string `json:"appVersion"`

	ExtraTagInfo   map[string]string      `json:"extraTagInfo"`
	ExtraFieldInfo map[string]interface{} `json:"extraFieldInfo"`

	// Deprecated: replaced by ExtraTagInfo
	ExtraInfo map[string]string `json:"extraInfo"`
}

type CheckUpgradeResponse struct {
	Versions                 []Version `json:"versions"`
	RequestIntervalInMinutes int       `json:"requestIntervalInMinutes"`
}

func NewUpgradeChecker(address string, upgradeRequester UpgradeRequester) *UpgradeChecker {
	return &UpgradeChecker{
		Address:                address,
		UpgradeRequester:       upgradeRequester,
		DefaultRequestInterval: 1 * time.Hour,
		stopCh:                 make(chan struct{}),
	}
}

func (c *UpgradeChecker) Start() {
	go c.run()
}

func (c *UpgradeChecker) run() {
	requestInterval := c.DefaultRequestInterval

	doWork := func() {
		resp, err := c.CheckUpgrade(c.UpgradeRequester.GetCurrentVersion(), c.UpgradeRequester.GetExtraInfo())
		if err == nil && resp.RequestIntervalInMinutes > 0 {
			requestInterval = time.Duration(resp.RequestIntervalInMinutes) * time.Minute
		}
		c.UpgradeRequester.ProcessUpgradeResponse(resp, err)
	}

	doWork()

	for {
		select {
		case <-time.After(requestInterval):
			doWork()
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

func (c *UpgradeChecker) SetDefaultRequestInterval(interval time.Duration) {
	c.DefaultRequestInterval = interval
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
		messageBytes, err := io.ReadAll(r.Body)
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
