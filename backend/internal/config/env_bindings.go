package config

import (
	"reflect"
	"strings"

	"github.com/spf13/viper"
)

// AutomaticEnv cannot introduce keys into AllKeys, so Unmarshal silently loses
// env-only scalar configuration unless defaults or explicit bindings exist.
// Bind missing keys rather than setting zero defaults: absence-sensitive IsSet
// fallbacks (sticky escape/trusted proxies) must retain their current semantics.
func bindEnvReachableConfigKeys() {
	registered := make(map[string]bool)
	for _, key := range viper.AllKeys() {
		registered[key] = true
	}
	var walk func(reflect.Type, string)
	walk = func(t reflect.Type, prefix string) {
		for i := 0; i < t.NumField(); i++ {
			field := t.Field(i)
			if field.PkgPath != "" {
				continue
			}
			name, _, _ := strings.Cut(field.Tag.Get("mapstructure"), ",")
			if name == "-" {
				continue
			}
			if name == "" {
				name = strings.ToLower(field.Name)
			}
			key := strings.ToLower(name)
			if prefix != "" {
				key = prefix + "." + key
			}
			kind := field.Type
			for kind.Kind() == reflect.Ptr {
				kind = kind.Elem()
			}
			if kind.Kind() == reflect.Struct {
				walk(kind, key)
				continue
			}
			if kind.Kind() == reflect.Map {
				continue
			}
			if kind.Kind() == reflect.Slice {
				elem := kind.Elem()
				for elem.Kind() == reflect.Ptr {
					elem = elem.Elem()
				}
				if elem.Kind() == reflect.Struct || elem.Kind() == reflect.Map {
					continue
				}
			}
			if !registered[key] {
				// The non-empty key is guaranteed above; BindEnv only fails for
				// missing arguments. Preserve existing aliases by never rebinding.
				_ = viper.BindEnv(key)
				registered[key] = true
			}
		}
	}
	walk(reflect.TypeOf(Config{}), "")
}
