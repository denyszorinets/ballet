// Package config loads service configuration from defaults, an optional TOML
// file and environment variables, in that order of precedence.
//
// Keys are addressed by their `toml` struct tags. The environment variable for
// a key is the prefix plus the upper-cased key path joined by underscores:
// with prefix BALLET_CORE, [server] addr becomes BALLET_CORE_SERVER_ADDR.
package config

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// Validator is implemented by configurations that check their own invariants.
type Validator interface {
	Validate() error
}

var durationType = reflect.TypeFor[time.Duration]()

// Load fills dst, a pointer to a struct pre-populated with defaults. Values from
// the TOML file at path (skipped when path is empty) override defaults;
// environment variables override the file. String lists are comma-separated
// in environment variables. Unknown file keys are rejected.
// If dst implements Validator, it is validated last.
func Load(path, envPrefix string, dst any) error {
	if path != "" {
		md, err := toml.DecodeFile(path, dst)
		if err != nil {
			return fmt.Errorf("parse config file %s: %w", path, err)
		}
		if undecoded := md.Undecoded(); len(undecoded) > 0 {
			keys := make([]string, len(undecoded))
			for i, k := range undecoded {
				keys[i] = k.String()
			}
			return fmt.Errorf("config file %s: unknown keys: %s", path, strings.Join(keys, ", "))
		}
	}

	var errs []error
	walk(envPrefix, reflect.ValueOf(dst).Elem(), func(env string, field reflect.Value) {
		raw, ok := os.LookupEnv(env)
		if !ok {
			return
		}
		if err := setFromString(field, raw); err != nil {
			errs = append(errs, fmt.Errorf("environment variable %s: %w", env, err))
		}
	})
	if err := errors.Join(errs...); err != nil {
		return err
	}

	if v, ok := dst.(Validator); ok {
		if err := v.Validate(); err != nil {
			return fmt.Errorf("invalid configuration: %w", err)
		}
	}
	return nil
}

// EnvNames lists the environment variable of every key in cfg, in field order.
func EnvNames(envPrefix string, cfg any) []string {
	var names []string
	walk(envPrefix, reflect.ValueOf(cfg).Elem(), func(env string, _ reflect.Value) {
		names = append(names, env)
	})
	return names
}

// walk calls visit for every leaf field with a toml tag, recursing into
// nested structs (TOML tables).
func walk(prefix string, v reflect.Value, visit func(env string, field reflect.Value)) {
	t := v.Type()
	for i := range t.NumField() {
		sf := t.Field(i)
		name, _, _ := strings.Cut(sf.Tag.Get("toml"), ",")
		if sf.Anonymous && name == "" && sf.Type.Kind() == reflect.Struct {
			walk(prefix, v.Field(i), visit) // embedded struct: fields are promoted
			continue
		}
		if name == "" || name == "-" || !sf.IsExported() {
			continue
		}
		env := prefix + "_" + strings.ToUpper(name)
		field := v.Field(i)
		if field.Kind() == reflect.Struct && field.Type() != durationType {
			walk(env, field, visit)
			continue
		}
		visit(env, field)
	}
}

func setFromString(field reflect.Value, raw string) error {
	if field.Type() == durationType {
		d, err := time.ParseDuration(raw)
		if err != nil {
			return err
		}
		field.SetInt(int64(d))
		return nil
	}
	switch field.Kind() {
	case reflect.Slice:
		if field.Type().Elem().Kind() != reflect.String {
			return fmt.Errorf("unsupported type %s", field.Type())
		}
		// Comma-separated; surrounding spaces and empty items are dropped.
		items := []string{}
		for item := range strings.SplitSeq(raw, ",") {
			if item = strings.TrimSpace(item); item != "" {
				items = append(items, item)
			}
		}
		field.Set(reflect.ValueOf(items))
	case reflect.String:
		field.SetString(raw)
	case reflect.Bool:
		b, err := strconv.ParseBool(raw)
		if err != nil {
			return err
		}
		field.SetBool(b)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, err := strconv.ParseInt(raw, 10, field.Type().Bits())
		if err != nil {
			return err
		}
		field.SetInt(n)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		n, err := strconv.ParseUint(raw, 10, field.Type().Bits())
		if err != nil {
			return err
		}
		field.SetUint(n)
	case reflect.Float32, reflect.Float64:
		f, err := strconv.ParseFloat(raw, field.Type().Bits())
		if err != nil {
			return err
		}
		field.SetFloat(f)
	default:
		return fmt.Errorf("unsupported type %s", field.Type())
	}
	return nil
}
