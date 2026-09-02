package upgraderesponder

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	stdErrors "errors"

	"github.com/Sirupsen/logrus"
	"github.com/pkg/errors"
)

var (
	templatePlaceholderRegexp = regexp.MustCompile(`\{([^}]+)\}`)
)

// multierrType provides a helper for aggregating errors using standard library errors.Join
type multierrType struct{}

var multierr = multierrType{}

func (multierrType) Append(errs ...error) error {
	return stdErrors.Join(errs...)
}

type ScarfService struct {
	endpointTemplates map[string]struct{}
	timeout           time.Duration
	httpClient        *http.Client
	enabled           bool
}

func NewScarfService(endpointTemplates []string, timeoutSeconds int) *ScarfService {
	dedupEndpointTemplates := make(map[string]struct{})
	for _, template := range endpointTemplates {
		template = strings.TrimSpace(template)
		if template != "" {
			dedupEndpointTemplates[template] = struct{}{}
		}
	}

	if len(dedupEndpointTemplates) == 0 {
		return &ScarfService{enabled: false}
	}

	timeout := time.Duration(timeoutSeconds) * time.Second
	return &ScarfService{
		endpointTemplates: dedupEndpointTemplates,
		timeout:           timeout,
		httpClient: &http.Client{
			Timeout: timeout,
		},
		enabled: true,
	}
}

func (s *ScarfService) SendEvent(appVersion string, templateVars map[string]string, clientIP string) {
	if !s.enabled {
		return
	}

	// safety copy
	templateVarsCopy := make(map[string]string, len(templateVars))
	for k, v := range templateVars {
		templateVarsCopy[k] = v
	}

	go func() {
		if err := s.sendEventSync(templateVarsCopy, clientIP); err != nil {
			logrus.Errorf("Failed to send Scarf.sh event for version %s: %v", appVersion, err)
		} else {
			logrus.Debugf("Successfully sent Scarf.sh event for version %s", appVersion)
		}
	}()
}

func (s *ScarfService) sendEventSync(templateVars map[string]string, clientIP string) error {
	var result error

	// NOTE:
	// We intentionally do not deduplicate requests based on the resolved URL here.
	// Each configured endpoint template is treated as an independent entry and
	// should result in its own request, even if different templates resolve to
	// similar URLs after variable substitution.
	//
	// Template-level deduplication is already handled in NewScarfService().
	for endpointTemplate := range s.endpointTemplates {
		url := substituteTemplateVars(endpointTemplate, templateVars)

		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			result = multierr.Append(result, errors.Wrapf(err, "failed to create request for endpoint %q", endpointTemplate))
			continue
		}

		req.Header.Set("X-Scarf-IP", clientIP)
		req.Header.Set("User-Agent", "upgrade-responder")

		logrus.Debugf("Sending request %s", url)

		resp, err := s.httpClient.Do(req)
		if err != nil {
			closeResponseBody(resp)
			result = multierr.Append(result, errors.Wrapf(err, "failed to send request to endpoint %q", endpointTemplate))
			continue
		}

		if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
			result = multierr.Append(result, errors.Errorf("received non-2xx response status %d from URL %q", resp.StatusCode, url))
		}

		closeResponseBody(resp)
	}

	return result
}

func closeResponseBody(resp *http.Response) {
	if resp != nil && resp.Body != nil {
		if err := resp.Body.Close(); err != nil {
			logrus.Warnf("Failed to close response body: %v", err)
		}
	}
}

// substituteTemplateVars replaces placeholders in the form `{key}` within the
// given template string using values from vars.
//
// Behavior:
//   - Each `{key}` placeholder is replaced with vars[key] if present.
//   - If a placeholder has no corresponding value in vars, it is replaced
//     with an empty string.
//   - Variables in vars that are not referenced in the template are ignored.
//   - Multiple occurrences of the same placeholder are all replaced.
//   - Values are path-escaped, with "+" encoded as "%2B" so that semver build
//     metadata such as "v1.36.4+rke2r1" is matched by the Scarf gateway.
func substituteTemplateVars(template string, vars map[string]string) string {
	return templatePlaceholderRegexp.ReplaceAllStringFunc(template, func(placeholder string) string {
		key := strings.Trim(placeholder, "{}")

		if value, ok := vars[key]; ok {
			return strings.ReplaceAll(url.PathEscape(value), "+", "%2B")
		}

		return ""
	})
}
