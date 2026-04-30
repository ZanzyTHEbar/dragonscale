package schema

import (
	"reflect"
	"testing"

	"charm.land/fantasy/internal/testcmp"
	"github.com/stretchr/testify/require"
)

func TestEnumSupport(t *testing.T) {
	// Test enum via struct tags
	type WeatherInput struct {
		Location string `json:"location" description:"City name"`
		Units    string `json:"units" enum:"celsius,fahrenheit,kelvin" description:"Temperature units"`
		Format   string `json:"format,omitempty" enum:"json,xml,text"`
	}

	schema := Generate(reflect.TypeFor[WeatherInput]())

	require.Equal(t, "object", schema.Type)

	// Check units field has enum values
	unitsSchema := schema.Properties["units"]
	require.NotNil(t, unitsSchema, "Expected units property to exist")
	testcmp.RequireEqual(t, []any{"celsius", "fahrenheit", "kelvin"}, unitsSchema.Enum)

	// Check required fields (format should not be required due to omitempty)
	expectedRequired := []string{"location", "units"}
	testcmp.RequireEqual(t, expectedRequired, schema.Required)
}

func TestSchemaToParameters(t *testing.T) {
	testSchema := Schema{
		Type: "object",
		Properties: map[string]*Schema{
			"name": {
				Type:        "string",
				Description: "The name field",
			},
			"age": {
				Type:    "integer",
				Minimum: func() *float64 { v := 0.0; return &v }(),
				Maximum: func() *float64 { v := 120.0; return &v }(),
			},
			"tags": {
				Type: "array",
				Items: &Schema{
					Type: "string",
				},
			},
			"priority": {
				Type: "string",
				Enum: []any{"low", "medium", "high"},
			},
		},
		Required: []string{"name"},
	}

	params := ToParameters(testSchema)

	expected := map[string]any{
		"name": map[string]any{
			"type":        "string",
			"description": "The name field",
		},
		"age": map[string]any{
			"type":    "integer",
			"minimum": 0.0,
			"maximum": 120.0,
		},
		"tags": map[string]any{
			"type": "array",
			"items": map[string]any{
				"type": "string",
			},
		},
		"priority": map[string]any{
			"type": "string",
			"enum": []any{"low", "medium", "high"},
		},
	}
	testcmp.RequireEqual(t, expected, params)
}

func TestGenerateSchemaBasicTypes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    any
		expected Schema
	}{
		{
			name:     "string type",
			input:    "",
			expected: Schema{Type: "string"},
		},
		{
			name:     "int type",
			input:    0,
			expected: Schema{Type: "integer"},
		},
		{
			name:     "int64 type",
			input:    int64(0),
			expected: Schema{Type: "integer"},
		},
		{
			name:     "uint type",
			input:    uint(0),
			expected: Schema{Type: "integer"},
		},
		{
			name:     "float64 type",
			input:    0.0,
			expected: Schema{Type: "number"},
		},
		{
			name:     "float32 type",
			input:    float32(0.0),
			expected: Schema{Type: "number"},
		},
		{
			name:     "bool type",
			input:    false,
			expected: Schema{Type: "boolean"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			schema := Generate(reflect.TypeOf(tt.input))
			testcmp.RequireEqual(t, tt.expected, schema)
		})
	}
}

func TestGenerateSchemaArrayTypes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    any
		expected Schema
	}{
		{
			name:  "string slice",
			input: []string{},
			expected: Schema{
				Type:  "array",
				Items: &Schema{Type: "string"},
			},
		},
		{
			name:  "int slice",
			input: []int{},
			expected: Schema{
				Type:  "array",
				Items: &Schema{Type: "integer"},
			},
		},
		{
			name:  "string array",
			input: [3]string{},
			expected: Schema{
				Type:  "array",
				Items: &Schema{Type: "string"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			schema := Generate(reflect.TypeOf(tt.input))
			require.NotNil(t, schema.Items, "Expected items schema to exist")
			testcmp.RequireEqual(t, tt.expected, schema)
		})
	}
}

func TestGenerateSchemaMapTypes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    any
		expected string
	}{
		{
			name:     "string to string map",
			input:    map[string]string{},
			expected: "object",
		},
		{
			name:     "string to int map",
			input:    map[string]int{},
			expected: "object",
		},
		{
			name:     "int to string map",
			input:    map[int]string{},
			expected: "object",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			schema := Generate(reflect.TypeOf(tt.input))
			require.Equal(t, tt.expected, schema.Type)
		})
	}
}

func TestGenerateSchemaStructTypes(t *testing.T) {
	t.Parallel()

	type SimpleStruct struct {
		Name string `json:"name" description:"The name field"`
		Age  int    `json:"age"`
	}

	type StructWithOmitEmpty struct {
		Required string `json:"required"`
		Optional string `json:"optional,omitempty"`
	}

	type StructWithJSONIgnore struct {
		Visible string `json:"visible"`
		Hidden  string `json:"-"`
	}

	type StructWithoutJSONTags struct {
		FirstName string
		LastName  string
	}

	tests := []struct {
		name     string
		input    any
		validate func(t *testing.T, schema Schema)
	}{
		{
			name:  "simple struct",
			input: SimpleStruct{},
			validate: func(t *testing.T, schema Schema) {
				require.Equal(t, "object", schema.Type)
				require.Len(t, schema.Properties, 2)
				require.NotNil(t, schema.Properties["name"], "Expected name property to exist")
				require.Equal(t, "The name field", schema.Properties["name"].Description)
				require.Len(t, schema.Required, 2)
			},
		},
		{
			name:  "struct with omitempty",
			input: StructWithOmitEmpty{},
			validate: func(t *testing.T, schema Schema) {
				testcmp.RequireEqual(t, []string{"required"}, schema.Required)
			},
		},
		{
			name:  "struct with json ignore",
			input: StructWithJSONIgnore{},
			validate: func(t *testing.T, schema Schema) {
				require.Len(t, schema.Properties, 1)
				require.NotNil(t, schema.Properties["visible"], "Expected visible property to exist")
				require.Nil(t, schema.Properties["hidden"], "Expected hidden property to not exist")
			},
		},
		{
			name:  "struct without json tags",
			input: StructWithoutJSONTags{},
			validate: func(t *testing.T, schema Schema) {
				require.NotNil(t, schema.Properties["first_name"], "Expected first_name property to exist")
				require.NotNil(t, schema.Properties["last_name"], "Expected last_name property to exist")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			schema := Generate(reflect.TypeOf(tt.input))
			tt.validate(t, schema)
		})
	}
}

func TestGenerateSchemaPointerTypes(t *testing.T) {
	t.Parallel()

	type StructWithPointers struct {
		Name *string `json:"name"`
		Age  *int    `json:"age"`
	}

	schema := Generate(reflect.TypeFor[StructWithPointers]())

	require.Equal(t, "object", schema.Type)

	actual := map[string]string{}
	for _, field := range []string{"name", "age"} {
		fieldSchema := schema.Properties[field]
		require.NotNil(t, fieldSchema, "Expected %s property to exist", field)
		actual[field] = fieldSchema.Type
	}
	testcmp.RequireEqual(t, map[string]string{
		"name": "string",
		"age":  "integer",
	}, actual)
}

func TestGenerateSchemaNestedStructs(t *testing.T) {
	t.Parallel()

	type Address struct {
		Street string `json:"street"`
		City   string `json:"city"`
	}

	type Person struct {
		Name    string  `json:"name"`
		Address Address `json:"address"`
	}

	schema := Generate(reflect.TypeFor[Person]())

	require.Equal(t, "object", schema.Type)

	require.NotNil(t, schema.Properties["address"], "Expected address property to exist")

	addressSchema := schema.Properties["address"]
	require.Equal(t, "object", addressSchema.Type)

	actual := map[string]string{}
	for _, field := range []string{"street", "city"} {
		fieldSchema := addressSchema.Properties[field]
		require.NotNil(t, fieldSchema, "Expected %s property in address to exist", field)
		actual[field] = fieldSchema.Type
	}
	testcmp.RequireEqual(t, map[string]string{
		"street": "string",
		"city":   "string",
	}, actual)
}

func TestGenerateSchemaRecursiveStructs(t *testing.T) {
	t.Parallel()

	type Node struct {
		Value string `json:"value"`
		Next  *Node  `json:"next,omitempty"`
	}

	schema := Generate(reflect.TypeFor[Node]())

	require.Equal(t, "object", schema.Type)

	require.NotNil(t, schema.Properties["value"], "Expected value property to exist")

	require.NotNil(t, schema.Properties["next"], "Expected next property to exist")

	// The recursive reference should be handled gracefully
	nextSchema := schema.Properties["next"]
	require.Equal(t, "object", nextSchema.Type)
}

func TestGenerateSchemaWithEnumTags(t *testing.T) {
	t.Parallel()

	type ConfigInput struct {
		Level    string `json:"level" enum:"debug,info,warn,error" description:"Log level"`
		Format   string `json:"format" enum:"json,text"`
		Optional string `json:"optional,omitempty" enum:"a,b,c"`
	}

	schema := Generate(reflect.TypeFor[ConfigInput]())

	tests := []struct {
		name     string
		field    string
		expected []any
	}{
		{
			name:     "level field",
			field:    "level",
			expected: []any{"debug", "info", "warn", "error"},
		},
		{
			name:     "format field",
			field:    "format",
			expected: []any{"json", "text"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			fieldSchema := schema.Properties[tt.field]
			require.NotNil(t, fieldSchema, "Expected %s property to exist", tt.field)
			testcmp.RequireEqual(t, tt.expected, fieldSchema.Enum)
		})
	}

	// Check required fields (optional should not be required due to omitempty)
	expectedRequired := []string{"level", "format"}
	testcmp.RequireEqual(t, expectedRequired, schema.Required)
}

func TestGenerateSchemaComplexTypes(t *testing.T) {
	t.Parallel()

	type ComplexInput struct {
		StringSlice []string            `json:"string_slice"`
		IntMap      map[string]int      `json:"int_map"`
		NestedSlice []map[string]string `json:"nested_slice"`
		Interface   any                 `json:"interface"`
	}

	schema := Generate(reflect.TypeFor[ComplexInput]())

	tests := []struct {
		field        string
		expectedType string
		expectedItem string
	}{
		{
			field:        "string_slice",
			expectedType: "array",
			expectedItem: "string",
		},
		{
			field:        "int_map",
			expectedType: "object",
		},
		{
			field:        "nested_slice",
			expectedType: "array",
			expectedItem: "object",
		},
		{
			field:        "interface",
			expectedType: "object",
		},
	}

	for _, tt := range tests {
		t.Run(tt.field, func(t *testing.T) {
			t.Parallel()
			fieldSchema := schema.Properties[tt.field]
			require.NotNil(t, fieldSchema, "Expected %s property to exist", tt.field)
			actual := map[string]string{"type": fieldSchema.Type}
			if tt.expectedItem != "" {
				require.NotNil(t, fieldSchema.Items, "Expected %s items schema to exist", tt.field)
				actual["items.type"] = fieldSchema.Items.Type
			}
			expected := map[string]string{"type": tt.expectedType}
			if tt.expectedItem != "" {
				expected["items.type"] = tt.expectedItem
			}
			testcmp.RequireEqual(t, expected, actual)
		})
	}
}

func TestToSnakeCase(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input    string
		expected string
	}{
		{"FirstName", "first_name"},
		{"XMLHttpRequest", "x_m_l_http_request"},
		{"ID", "i_d"},
		{"HTTPSProxy", "h_t_t_p_s_proxy"},
		{"simple", "simple"},
		{"", ""},
		{"A", "a"},
		{"AB", "a_b"},
		{"CamelCase", "camel_case"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			t.Parallel()
			result := toSnakeCase(tt.input)
			require.Equal(t, tt.expected, result, "toSnakeCase(%s)", tt.input)
		})
	}
}

func TestSchemaToParametersEdgeCases(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		schema   Schema
		expected map[string]any
	}{
		{
			name: "non-object schema",
			schema: Schema{
				Type: "string",
			},
			expected: map[string]any{},
		},
		{
			name: "object with no properties",
			schema: Schema{
				Type:       "object",
				Properties: nil,
			},
			expected: map[string]any{},
		},
		{
			name: "object with empty properties",
			schema: Schema{
				Type:       "object",
				Properties: map[string]*Schema{},
			},
			expected: map[string]any{},
		},
		{
			name: "schema with all constraint types",
			schema: Schema{
				Type: "object",
				Properties: map[string]*Schema{
					"text": {
						Type:      "string",
						Format:    "email",
						MinLength: func() *int { v := 5; return &v }(),
						MaxLength: func() *int { v := 100; return &v }(),
					},
					"number": {
						Type:    "number",
						Minimum: func() *float64 { v := 0.0; return &v }(),
						Maximum: func() *float64 { v := 100.0; return &v }(),
					},
				},
			},
			expected: map[string]any{
				"text": map[string]any{
					"type":      "string",
					"format":    "email",
					"minLength": 5,
					"maxLength": 100,
				},
				"number": map[string]any{
					"type":    "number",
					"minimum": 0.0,
					"maximum": 100.0,
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := ToParameters(tt.schema)
			testcmp.RequireEqual(t, tt.expected, result)
		})
	}
}

func TestNormalize_TypeArray(t *testing.T) {
	t.Parallel()

	node := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"value": map[string]any{
				"description": "Config value",
				"type":        []any{"string", "number", "boolean", "object", "array", "null"},
			},
		},
	}

	Normalize(node)

	val := node["properties"].(map[string]any)["value"].(map[string]any)
	require.Nil(t, val["type"])
	anyOf, ok := val["anyOf"].([]any)
	require.True(t, ok)
	require.Len(t, anyOf, 6)

	for _, v := range anyOf {
		variant := v.(map[string]any)
		if variant["type"] == "array" {
			require.Contains(t, variant, "items")
		}
	}
	require.Equal(t, "Config value", val["description"])
}

func TestNormalize_SingleStringType(t *testing.T) {
	t.Parallel()

	node := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{"type": "string"},
		},
	}

	Normalize(node)

	val := node["properties"].(map[string]any)["name"].(map[string]any)
	require.Equal(t, "string", val["type"])
}

func TestNormalize_BareArrayGetsItems(t *testing.T) {
	t.Parallel()

	node := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"tags": map[string]any{"type": "array"},
		},
	}

	Normalize(node)

	val := node["properties"].(map[string]any)["tags"].(map[string]any)
	require.Equal(t, "array", val["type"])
	require.Contains(t, val, "items")
}

func TestNormalize_SingleElementTypeArray(t *testing.T) {
	t.Parallel()

	node := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{"type": []any{"string"}},
		},
	}

	Normalize(node)

	val := node["properties"].(map[string]any)["name"].(map[string]any)
	require.Nil(t, val["type"])
	anyOf, ok := val["anyOf"].([]any)
	require.True(t, ok)
	require.Len(t, anyOf, 1)
	require.Equal(t, "string", anyOf[0].(map[string]any)["type"])
}

func TestNormalize_NestedProperties(t *testing.T) {
	t.Parallel()

	node := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"config": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"val": map[string]any{"type": []any{"string", "number"}},
				},
			},
		},
	}

	Normalize(node)

	val := node["properties"].(map[string]any)["config"].(map[string]any)["properties"].(map[string]any)["val"].(map[string]any)
	require.Nil(t, val["type"])
	require.NotNil(t, val["anyOf"])
}
