// Package config loads and validates the monitor settings from environment
// variables: caarlos0/env parses them, go-playground/validator checks them.
package config

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/caarlos0/env/v11"
	"github.com/go-playground/locales/en"
	ut "github.com/go-playground/universal-translator"
	"github.com/go-playground/validator/v10"
	entranslations "github.com/go-playground/validator/v10/translations/en"

	"github.com/a1ekseev/autoheal/internal/logging"
)

// Config holds the environment variables read by the original monitor.py.
type Config struct {
	CheckIntervalSeconds int           `env:"CHECK_INTERVAL" envDefault:"10"   validate:"gte=1,lte=86400"`
	FailThreshold        int           `env:"FAIL_THRESHOLD" envDefault:"3"    validate:"gte=1"`
	PingTimeoutSeconds   float64       `env:"PING_TIMEOUT"   envDefault:"10"   validate:"gt=0,lte=86400"`
	CurlTimeoutSeconds   float64       `env:"CURL_TIMEOUT"   envDefault:"10"   validate:"gt=0,lte=86400"`
	LogLevel             logging.Level `env:"LOG_LEVEL"      envDefault:"INFO"`
}

// CheckInterval is the pause between two monitoring cycles.
func (c Config) CheckInterval() time.Duration {
	return time.Duration(c.CheckIntervalSeconds) * time.Second
}

// PingTimeout bounds a single ICMP check.
func (c Config) PingTimeout() time.Duration { return seconds(c.PingTimeoutSeconds) }

// CurlTimeout bounds a single HTTP check.
func (c Config) CurlTimeout() time.Duration { return seconds(c.CurlTimeoutSeconds) }

func seconds(f float64) time.Duration {
	return time.Duration(f * float64(time.Second))
}

var (
	checker    = validator.New(validator.WithRequiredStructEnabled())
	translator ut.Translator
)

func init() {
	// Report fields by their environment variable name.
	checker.RegisterTagNameFunc(func(f reflect.StructField) string {
		name, _, _ := strings.Cut(f.Tag.Get("env"), ",")
		return name
	})

	locale := en.New()
	translator, _ = ut.New(locale, locale).GetTranslator("en")
	if err := entranslations.RegisterDefaultTranslations(checker, translator); err != nil {
		panic(err)
	}
}

// Load parses environ (as returned by env.ToMap(os.Environ())) and validates
// it. Parse and validation problems are reported together; a variable that
// is set but empty falls back to its default.
func Load(environ map[string]string) (Config, error) {
	var cfg Config
	unparsed, parseErr := parse(&cfg, environ)
	errs := append([]error{parseErr}, validate(cfg, unparsed)...)
	if err := errors.Join(errs...); err != nil {
		return Config{}, fmt.Errorf("invalid configuration: %w", err)
	}
	return cfg, nil
}

// parse fills cfg and returns the struct field names that failed to parse.
func parse(cfg *Config, environ map[string]string) ([]string, error) {
	err := env.ParseWithOptions(cfg, env.Options{Environment: environ})
	agg, ok := errors.AsType[env.AggregateError](err)
	if !ok {
		return nil, err //nolint:wrapcheck // nil or a non-aggregate env error, already descriptive
	}

	var unparsed []string
	errs := make([]error, 0, len(agg.Errors))
	for _, e := range agg.Errors {
		if pe, ok := errors.AsType[env.ParseError](e); ok {
			unparsed = append(unparsed, pe.Name)
			e = fmt.Errorf("%s must be a valid %s: %w", envName(pe.Name), pe.Type, pe.Err)
		}
		errs = append(errs, e)
	}
	return unparsed, errors.Join(errs...)
}

// validate checks cfg with the struct tags, skipping fields that failed to parse.
func validate(cfg Config, skip []string) []error {
	err := checker.StructExcept(cfg, skip...)
	verrs, ok := errors.AsType[validator.ValidationErrors](err)
	if !ok {
		if err != nil {
			return []error{fmt.Errorf("validate configuration: %w", err)}
		}
		return nil
	}
	out := make([]error, 0, len(verrs))
	for _, fe := range verrs {
		out = append(out, fmt.Errorf("%s, got %v", fe.Translate(translator), fe.Value()))
	}
	return out
}

func envName(field string) string {
	f, ok := reflect.TypeFor[Config]().FieldByName(field)
	if !ok {
		return field
	}
	name, _, _ := strings.Cut(f.Tag.Get("env"), ",")
	return name
}
