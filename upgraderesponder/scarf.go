package upgraderesponder

import (
	"net/http"
	"strings"
	"time"

	"github.com/Sirupsen/logrus"
)

type ScarfService struct {
	endpointTemplate string
	timeout          time.Duration
	httpClient       *http.Client
	enabled          bool
}

func NewScarfService(endpointTemplate string, timeoutSeconds int) *ScarfService {
	if endpointTemplate == "" {
		return &ScarfService{enabled: false}
	}

	timeout := time.Duration(timeoutSeconds) * time.Second
	return &ScarfService{
		endpointTemplate: endpointTemplate,
		timeout:          timeout,
		httpClient: &http.Client{
			Timeout: timeout,
		},
		enabled: true,
	}
}

func (s *ScarfService) SendEvent(appVersion, clientIP string) {
	if !s.enabled {
		return
	}

	go func() {
		if err := s.sendEventSync(appVersion, clientIP); err != nil {
			logrus.Errorf("Failed to send Scarf.sh event for version %s: %v", appVersion, err)
		} else {
			logrus.Debugf("Successfully sent Scarf.sh event for version %s", appVersion)
		}
	}()
}

func (s *ScarfService) sendEventSync(appVersion, clientIP string) error {
	url := strings.Replace(s.endpointTemplate, "{version}", appVersion, -1)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}

	req.Header.Set("X-Scarf-IP", clientIP)
	req.Header.Set("User-Agent", "upgrade-responder")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return nil
}
