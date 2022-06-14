package upgraderesponder

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_GetVersion(t *testing.T) {
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
			input:  CheckUpgradeRequest{AppVersion: "v1.0.0", HarvesterVersion: "v0.0.0"},
			output: "v1.0.0",
		},
		{
			name:   "harvesterVersion only",
			input:  CheckUpgradeRequest{HarvesterVersion: "v1.0.0"},
			output: "v1.0.0",
		},
	}

	for _, testCase := range testCases {
		result := testCase.input.GetVersion()
		assert.Equal(t, testCase.output, result)
	}
}
