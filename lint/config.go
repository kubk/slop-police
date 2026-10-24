package lint

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
)

const configFileName = "slop-police.json"

// ConfigError is reported on stderr with exit code 2.
type ConfigError struct{ Message string }

func (e *ConfigError) Error() string { return e.Message }

func configErrorf(format string, args ...any) error {
	return &ConfigError{fmt.Sprintf(format, args...)}
}

type Severity string

type EnabledRule struct {
	Rule     *Rule
	Severity Severity
	Options  Options
}

type Config struct {
	Projects  []string
	Ignore    []string
	TestFiles []string
	Rules     []EnabledRule
}

// LoadConfig reads and validates slop-police.json in root. pluginRules are
// the rules the npm launcher loaded from the config's plugins, or nil when
// slop-police runs without the launcher.
func LoadConfig(root string, pluginRules []*Rule) (*Config, error) {
	data, err := os.ReadFile(filepath.Join(root, configFileName))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, configErrorf("Config file not found: %s", configFileName)
		}
		return nil, configErrorf("Unable to read %s: %v", configFileName, err)
	}
	return ParseConfig(data, pluginRules)
}

// ParseConfig validates a config file's contents (see LoadConfig).
func ParseConfig(data []byte, pluginRules []*Rule) (*Config, error) {
	var raw map[string]json.RawMessage
	err := json.Unmarshal(data, &raw)
	if err != nil && !errors.As(err, new(*json.UnmarshalTypeError)) {
		return nil, configErrorf("Unable to parse %s: %v", configFileName, err)
	}
	if raw == nil {
		return nil, configErrorf("Config must be an object")
	}
	for _, key := range sortedKeys(raw) {
		switch key {
		case "projects", "plugins", "ignore", "testFiles", "rules":
		default:
			return nil, configErrorf("Unknown config key: %s", key)
		}
	}

	config := &Config{}
	if err := unmarshalOptional(raw["projects"], &config.Projects); err != nil || len(config.Projects) == 0 {
		return nil, configErrorf(`"projects" must be a non-empty array of tsconfig paths`)
	}
	var plugins []string
	if err := unmarshalOptional(raw["plugins"], &plugins); err != nil {
		return nil, configErrorf(`"plugins" must be an array of module paths`)
	}
	if len(plugins) > 0 && pluginRules == nil {
		return nil, configErrorf("Plugins are loaded by the npm launcher: run slop-police with npx or pnpm exec")
	}
	pluginRulesByName := map[string]*Rule{}
	for _, rule := range pluginRules {
		if registry[rule.Name] != nil {
			return nil, configErrorf("Plugin rule %s has the same name as a built-in rule", rule.Name)
		}
		pluginRulesByName[rule.Name] = rule
	}
	if err := unmarshalOptional(raw["ignore"], &config.Ignore); err != nil {
		return nil, configErrorf(`"ignore" must be an array of glob patterns`)
	}
	if err := unmarshalOptional(raw["testFiles"], &config.TestFiles); err != nil {
		return nil, configErrorf(`"testFiles" must be an array of glob patterns`)
	}
	var entries map[string]json.RawMessage
	if err := unmarshalOptional(raw["rules"], &entries); err != nil {
		return nil, configErrorf(`"rules" must be an object`)
	}

	for _, name := range sortedKeys(entries) {
		rule := registry[name]
		if rule == nil {
			rule = pluginRulesByName[name]
		}
		if rule == nil {
			return nil, configErrorf("Unknown rule: %s", name)
		}
		severity, options, err := parseRuleEntry(entries[name])
		if err != nil {
			return nil, configErrorf("Rule %s %v", name, err)
		}
		merged := maps.Clone(rule.DefaultOptions)
		if merged == nil {
			merged = Options{}
		}
		for _, key := range sortedKeys(options) {
			def, ok := rule.DefaultOptions[key]
			if !ok {
				return nil, configErrorf("Unknown option for rule %s: %s", name, key)
			}
			value, ok := decodeOption(def, options[key])
			if !ok {
				return nil, configErrorf("Rule %s option %s must be %s", name, key, jsonType(def))
			}
			merged[key] = value
		}
		if severity != "off" {
			config.Rules = append(config.Rules, EnabledRule{Rule: rule, Severity: severity, Options: merged})
		}
	}
	return config, nil
}

// parseRuleEntry accepts "severity" or ["severity", {options}].
func parseRuleEntry(entry json.RawMessage) (Severity, map[string]json.RawMessage, error) {
	var severity Severity
	var options map[string]json.RawMessage
	if bytes.HasPrefix(bytes.TrimSpace(entry), []byte("[")) {
		var parts []json.RawMessage
		if err := json.Unmarshal(entry, &parts); err != nil || len(parts) == 0 || len(parts) > 2 {
			return "", nil, fmt.Errorf(`must be a severity or ["severity", {options}]`)
		}
		entry = parts[0]
		if len(parts) == 2 {
			if err := json.Unmarshal(parts[1], &options); err != nil || options == nil {
				return "", nil, fmt.Errorf("options must be an object")
			}
		}
	}
	if err := json.Unmarshal(entry, &severity); err != nil || (severity != "error" && severity != "warn" && severity != "off") {
		return "", nil, fmt.Errorf(`severity must be "error", "warn" or "off"`)
	}
	return severity, options, nil
}

// decodeOption decodes an option's value, which must have the type of its
// default def.
func decodeOption(def any, raw json.RawMessage) (any, bool) {
	if _, ok := def.([]string); ok {
		var list []string
		err := json.Unmarshal(raw, &list)
		return list, err == nil && list != nil
	}
	value := def
	err := json.Unmarshal(raw, &value)
	return value, err == nil && jsonType(value) == jsonType(def)
}

// jsonType names the JSON type of a decoded value.
func jsonType(value any) string {
	switch value.(type) {
	case bool:
		return "a boolean"
	case float64:
		return "a number"
	case string:
		return "a string"
	case []string:
		return "an array of strings"
	case []any:
		return "an array"
	case map[string]any:
		return "an object"
	}
	return "null"
}

// unmarshalOptional decodes value into target unless it is missing.
func unmarshalOptional(value json.RawMessage, target any) error {
	if value == nil {
		return nil
	}
	if string(bytes.TrimSpace(value)) == "null" {
		return fmt.Errorf("null")
	}
	return json.Unmarshal(value, target)
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}
