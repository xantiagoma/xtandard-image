package config

import (
	"encoding"
	"reflect"
	"strings"
)

// envPrefix is the prefix of every variable this package reads.
const envPrefix = "XI_"

// Loader variables: they steer Load and are not part of Config.
const (
	envConfigFile   = "XI_CONFIG_FILE"
	envAllowUnknown = "XI_ALLOW_UNKNOWN_ENV"
	envSources      = "XI_SOURCES"
)

// field is one leaf of the Config tree that an env variable can set.
type field struct {
	Env   string       // full variable name, e.g. XI_SERVER_ADDR
	YAML  string       // dotted YAML path, e.g. server.addr
	Index []int        // reflect.Value.FieldByIndex path from the walked root
	Type  reflect.Type // field type
}

var textUnmarshalerType = reflect.TypeFor[encoding.TextUnmarshaler]()

// fieldsOf lists the env-settable leaves of struct type t, computing names
// exactly as caarlos0/env does (prefix + envPrefix tags + env tag).
func fieldsOf(t reflect.Type, envPfx, yamlPfx string) []field {
	var out []field
	walkFields(t, envPfx, yamlPfx, nil, &out)
	return out
}

func walkFields(t reflect.Type, envPfx, yamlPfx string, index []int, out *[]field) {
	for i := range t.NumField() {
		sf := t.Field(i)
		if !sf.IsExported() {
			continue
		}
		envTag, _, _ := strings.Cut(sf.Tag.Get("env"), ",")
		if envTag == "-" {
			continue
		}
		yamlName, yamlOpts, _ := strings.Cut(sf.Tag.Get("yaml"), ",")
		path := yamlPfx
		if yamlOpts != "inline" {
			path = joinPath(yamlPfx, yamlName)
		}
		idx := append(append([]int(nil), index...), i)
		pfx, hasPfx := sf.Tag.Lookup("envPrefix")
		isStruct := sf.Type.Kind() == reflect.Struct && !reflect.PointerTo(sf.Type).Implements(textUnmarshalerType)
		if isStruct && (hasPfx || sf.Anonymous) {
			walkFields(sf.Type, envPfx+pfx, path, idx, out)
			continue
		}
		if envTag == "" {
			continue
		}
		*out = append(*out, field{Env: envPfx + envTag, YAML: path, Index: idx, Type: sf.Type})
	}
}

func joinPath(a, b string) string {
	if a == "" {
		return b
	}
	return a + "." + b
}

// staticFields are the leaves of Config (everything except named sources).
func staticFields() []field {
	return fieldsOf(reflect.TypeFor[Config](), envPrefix, "")
}

// sourceEnvPrefix returns the variable prefix of source name.
func sourceEnvPrefix(name string) string {
	return envSources + "_" + strings.ToUpper(strings.ReplaceAll(name, "-", "_")) + "_"
}

// sourceFields are the leaves of the named source name.
func sourceFields(name string) []field {
	return fieldsOf(reflect.TypeFor[Source](), sourceEnvPrefix(name), "sources."+name)
}

// envNameOf returns the variable for a YAML path by the mapping rule; list
// indexes ("keys[1]") are dropped.
func envNameOf(path string) string {
	var sb strings.Builder
	inIndex := false
	for _, c := range path {
		switch {
		case c == '[':
			inIndex = true
		case c == ']':
			inIndex = false
		case inIndex:
		case c == '.' || c == '-':
			sb.WriteByte('_')
		default:
			sb.WriteRune(c)
		}
	}
	return envPrefix + strings.ToUpper(sb.String())
}
