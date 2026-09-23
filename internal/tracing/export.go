package tracing

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"go.opentelemetry.io/collector/pdata/ptrace/ptraceotlp"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"go.opentelemetry.io/collector/pdata/ptrace"
)

type remoteExporter struct {
	endpoint string
	headers  http.Header
	client   *http.Client
}

func remoteFromEnv() (*remoteExporter, error) {
	first := func(a, b string) string {
		if v, ok := os.LookupEnv(a); ok {
			return v
		}
		return os.Getenv(b)
	}
	endpoint := strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"))
	specific := endpoint != ""
	if !specific {
		endpoint = strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"))
	}
	if endpoint == "" {
		return nil, nil
	}
	invalid := errors.New("invalid remote OTLP configuration")
	protocol := first("OTEL_EXPORTER_OTLP_TRACES_PROTOCOL", "OTEL_EXPORTER_OTLP_PROTOCOL")
	if protocol != "" && protocol != "http/protobuf" {
		return nil, invalid
	}
	u, err := url.Parse(endpoint)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, invalid
	}
	if !specific {
		u.Path = strings.TrimRight(u.Path, "/") + "/v1/traces"
	}
	headers := http.Header{}
	for _, part := range strings.Split(first("OTEL_EXPORTER_OTLP_TRACES_HEADERS", "OTEL_EXPORTER_OTLP_HEADERS"), ",") {
		if strings.TrimSpace(part) == "" {
			continue
		}
		k, v, ok := strings.Cut(part, "=")
		k = strings.TrimSpace(k)
		v, err = url.PathUnescape(strings.TrimSpace(v))
		if !ok || err != nil || !headerPattern.MatchString(k) || strings.ContainsAny(v, "\r\n") {
			return nil, invalid
		}
		headers.Set(k, v)
	}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
	if cert := first("OTEL_EXPORTER_OTLP_TRACES_CERTIFICATE", "OTEL_EXPORTER_OTLP_CERTIFICATE"); cert != "" {
		pem, e := os.ReadFile(cert)
		if e != nil {
			return nil, invalid
		}
		roots, e := x509.SystemCertPool()
		if e != nil {
			roots = x509.NewCertPool()
		}
		if !roots.AppendCertsFromPEM(pem) {
			return nil, invalid
		}
		tlsConfig.RootCAs = roots
	}
	clientCert := first("OTEL_EXPORTER_OTLP_TRACES_CLIENT_CERTIFICATE", "OTEL_EXPORTER_OTLP_CLIENT_CERTIFICATE")
	clientKey := first("OTEL_EXPORTER_OTLP_TRACES_CLIENT_KEY", "OTEL_EXPORTER_OTLP_CLIENT_KEY")
	if clientCert != "" || clientKey != "" {
		cert, e := tls.LoadX509KeyPair(clientCert, clientKey)
		if e != nil {
			return nil, invalid
		}
		tlsConfig.Certificates = []tls.Certificate{cert}
	}
	return &remoteExporter{u.String(), headers, &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{TLSClientConfig: tlsConfig}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func (e *remoteExporter) push(ctx context.Context, t ptrace.Traces) error {
	data, err := (&ptrace.ProtoMarshaler{}).MarshalTraces(t)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", e.endpoint, bytes.NewReader(data))
	if err != nil {
		return errors.New("remote export failed")
	}
	req.Header = e.headers.Clone()
	req.Header.Set("Content-Type", "application/x-protobuf")
	resp, err := e.client.Do(req)
	if err != nil {
		return errors.New("remote export failed")
	}
	defer resp.Body.Close()
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 65537))
	if readErr != nil || len(body) > 65536 {
		return errors.New("remote export response invalid")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return errors.New("remote export failed")
	}
	if len(body) > 0 {
		response := ptraceotlp.NewExportResponse()
		var decodeErr error
		if strings.Contains(resp.Header.Get("Content-Type"), "json") {
			var v struct {
				PartialSuccess struct {
					RejectedSpans json.RawMessage `json:"rejectedSpans"`
				}
			}
			decodeErr = json.Unmarshal(body, &v)
			if string(v.PartialSuccess.RejectedSpans) != "" && string(v.PartialSuccess.RejectedSpans) != "0" && string(v.PartialSuccess.RejectedSpans) != `"0"` {
				return errors.New("remote export partially rejected")
			}
		} else {
			decodeErr = response.UnmarshalProto(body)
			if decodeErr == nil && response.PartialSuccess().RejectedSpans() > 0 {
				return errors.New("remote export partially rejected")
			}
		}
		if decodeErr != nil {
			return errors.New("remote export response invalid")
		}
	}
	return nil
}
