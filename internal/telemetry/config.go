package telemetry

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

type Config struct {
	Endpoint string            `json:"endpoint"`
	Headers  map[string]string `json:"headers,omitempty"`
}

var headerName = regexp.MustCompile("^[!#$%&'*+.^_`|~0-9a-zA-Z-]+$")

func FromEnvironment(env []string) (*Config, error) {
	values := map[string]string{}
	for _, entry := range env {
		k, v, ok := strings.Cut(entry, "=")
		if ok {
			values[k] = v
		}
	}
	endpoint := strings.TrimSpace(values["OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"])
	specific := endpoint != ""
	if !specific {
		endpoint = strings.TrimSpace(values["OTEL_EXPORTER_OTLP_ENDPOINT"])
	}
	if endpoint == "" {
		return nil, nil
	}
	protocol := values["OTEL_EXPORTER_OTLP_TRACES_PROTOCOL"]
	if protocol == "" {
		protocol = values["OTEL_EXPORTER_OTLP_PROTOCOL"]
	}
	if protocol != "" && protocol != "http/protobuf" {
		return nil, fmt.Errorf("OTLP requires http/protobuf")
	}
	u, err := url.Parse(endpoint)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("invalid OTLP endpoint")
	}
	if !specific {
		u.Path = strings.TrimRight(u.Path, "/") + "/v1/traces"
	}
	headers := values["OTEL_EXPORTER_OTLP_TRACES_HEADERS"]
	if headers == "" {
		headers = values["OTEL_EXPORTER_OTLP_HEADERS"]
	}
	result := &Config{Endpoint: u.String(), Headers: map[string]string{}}
	for _, pair := range strings.Split(headers, ",") {
		if strings.TrimSpace(pair) == "" {
			continue
		}
		name, value, ok := strings.Cut(pair, "=")
		name = strings.TrimSpace(name)
		value, err = url.PathUnescape(strings.TrimSpace(value))
		if !ok || err != nil || !headerName.MatchString(name) {
			return nil, fmt.Errorf("invalid OTLP header")
		}
		for _, r := range value {
			if r < 32 || r > 126 {
				return nil, fmt.Errorf("invalid OTLP header value")
			}
		}
		result.Headers[name] = value
	}
	return result, nil
}
