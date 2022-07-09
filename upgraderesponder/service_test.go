package upgraderesponder

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_GetAppVersion(t *testing.T) {
	var testCases = []struct {
		name   string
		input  CheckUpgradeRequest
		output string
	}{
		{
			name:   "appVersion only",
			input:  CheckUpgradeRequest{AppVersion: "v1.0.0"},
			output: "v1.0.0",
		},
		{
			name:   "appVersion and harvesterVersion",
			input:  CheckUpgradeRequest{AppVersion: "v1.0.0", LonghornVersion: "v0.0.0"},
			output: "v1.0.0",
		},
		{
			name:   "harvesterVersion only",
			input:  CheckUpgradeRequest{LonghornVersion: "v1.0.0"},
			output: "v1.0.0",
		},
	}

	for _, testCase := range testCases {
		result := testCase.input.GetAppVersion()
		assert.Equal(t, testCase.output, result)
	}
}
