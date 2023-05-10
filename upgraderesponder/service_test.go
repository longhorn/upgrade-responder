package upgraderesponder

import "testing"

func TestValidate(t *testing.T) {
	testCases := []struct {
		schema   Schema
		value    interface{}
		expected bool
	}{
		{
			schema:   Schema{DataType: "string", MaxLen: 5},
			value:    "1234",
			expected: true,
		},
		{
			schema:   Schema{DataType: "string", MaxLen: 5},
			value:    "123456",
			expected: false,
		},
		{
			schema:   Schema{DataType: "string"},
			value:    "123456",
			expected: true,
		},
		{
			schema:   Schema{DataType: "string"},
			value:    1234,
			expected: false,
		},
		{
			schema:   Schema{DataType: "string"},
			value:    1.0,
			expected: false,
		},
		{
			schema:   Schema{DataType: "float"},
			value:    1,
			expected: false,
		},
		{
			schema:   Schema{DataType: "float"},
			value:    "1",
			expected: false,
		},
		{
			schema:   Schema{DataType: "float"},
			value:    false,
			expected: false,
		},
		{
			schema:   Schema{DataType: "float"},
			value:    1.0,
			expected: true,
		},
		{
			schema:   Schema{DataType: "invalid"},
			value:    1.0,
			expected: false,
		},
		{
			schema:   Schema{DataType: "int"},
			value:    1.0,
			expected: false,
		},
		{
			schema:   Schema{DataType: "int"},
			value:    "1",
			expected: false,
		},
		{
			schema:   Schema{DataType: "int"},
			value:    1,
			expected: false,
		},
	}

	for i, testCase := range testCases {
		if output := testCase.schema.Validate(testCase.value); output != testCase.expected {
			t.Errorf("Test case %v: %+v Output %v not equal to expected %v", i, testCase, output, testCase.expected)
		}
	}
}

func TestValidateAndLoadRequestSchema(t *testing.T) {
	s := Server{}

	testCases := []struct {
		requestSchema RequestSchema
		expectedError bool
	}{
		{
			requestSchema: RequestSchema{AppVersionSchema: Schema{DataType: "float"}},
			expectedError: true,
		},
		{
			requestSchema: RequestSchema{AppVersionSchema: Schema{DataType: "string", MaxLen: -1}},
			expectedError: true,
		},
		{
			requestSchema: RequestSchema{AppVersionSchema: Schema{DataType: "string", MaxLen: 10}},
			expectedError: false,
		},
		{
			requestSchema: RequestSchema{
				AppVersionSchema: Schema{DataType: "string", MaxLen: 10},
				ExtraTagInfoSchema: map[string]Schema{
					"tag-1": {
						DataType: "boolean",
					},
				},
			},
			expectedError: true,
		},
		{
			requestSchema: RequestSchema{
				AppVersionSchema: Schema{DataType: "string", MaxLen: 10},
				ExtraTagInfoSchema: map[string]Schema{
					"tag-1": {DataType: "string"},
				},
				ExtraFieldInfoSchema: map[string]Schema{
					"field-1": {DataType: "string"},
					"field-2": {DataType: "float"},
					"field-3": {DataType: "int"},
				},
			},
			expectedError: true,
		},
		{
			requestSchema: RequestSchema{
				AppVersionSchema: Schema{DataType: "string", MaxLen: 10},
				ExtraTagInfoSchema: map[string]Schema{
					"tag-1": {DataType: "string"},
				},
				ExtraFieldInfoSchema: map[string]Schema{
					"field-1": {DataType: "string"},
					"field-2": {DataType: "float"},
					"field-3": {DataType: "boolean"},
				},
			},
			expectedError: false,
		},
	}

	boolToString := func(b bool) string {
		if b {
			return ""
		}
		return "no "
	}
	for i, testCase := range testCases {
		err := s.validateAndLoadRequestSchema(testCase.requestSchema)
		if testCase.expectedError != (err != nil) {
			t.Errorf("Test case %v : %v expected %verror but got %v", i, testCase, boolToString(testCase.expectedError), err)
		}
	}
}

func TestValidateExtraInfo(t *testing.T) {
	s := Server{}
	s.RequestSchema = RequestSchema{
		AppVersionSchema: Schema{DataType: "string", MaxLen: 10},
		ExtraTagInfoSchema: map[string]Schema{
			"tag-1": {DataType: "string", MaxLen: 5},
		},
		ExtraFieldInfoSchema: map[string]Schema{
			"field-1": {DataType: "string"},
			"field-2": {DataType: "float"},
			"field-3": {DataType: "boolean"},
		},
	}

	testCases := []struct {
		key           string
		value         interface{}
		extraInfoType string
		expected      bool
	}{
		{key: "tag-1", value: "1234", extraInfoType: extraInfoTypeTag, expected: true},
		{key: "tag-1", value: "123456", extraInfoType: extraInfoTypeTag, expected: false},
		{key: "tag-x", value: "1234", extraInfoType: extraInfoTypeTag, expected: false},
		{key: "field-1", value: "1234", extraInfoType: extraInfoTypeField, expected: true},
		{key: "field-1", value: 1234, extraInfoType: extraInfoTypeField, expected: false},
		{key: "field-x", value: 1234, extraInfoType: extraInfoTypeField, expected: false},
	}

	for i, testCase := range testCases {
		if output := s.ValidateExtraInfo(testCase.key, testCase.value, testCase.extraInfoType); output != testCase.expected {
			t.Errorf("Test case %v: %+v Output %v not equal to expected %v", i, testCase, output, testCase.expected)
		}
	}
}
