package fetcher

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

const (
	defaultDoHTimeout          = 5 * time.Second
	defaultDoHMaxResponseBytes = 64 * 1024
)

type DoHConfig struct {
	Endpoint         string
	BootstrapAddress string
}

type doHResolver struct {
	endpoint         string
	client           *http.Client
	maxResponseBytes int64
}

func NewDoHResolver(config DoHConfig) (Resolver, error) {
	endpoint, err := url.Parse(strings.TrimSpace(config.Endpoint))
	if err != nil || endpoint.Scheme != "https" || endpoint.Hostname() == "" || endpoint.User != nil ||
		endpoint.Fragment != "" || endpoint.RawQuery != "" {
		return nil, errors.New("DoH endpoint must be an HTTPS URL without credentials, query, or fragment")
	}
	endpointPort := endpoint.Port()
	if endpointPort == "" {
		endpointPort = "443"
	}
	if port, err := strconv.Atoi(endpointPort); err != nil || port < 1 || port > 65535 {
		return nil, errors.New("DoH endpoint has an invalid port")
	}

	bootstrapHost, bootstrapPort, err := net.SplitHostPort(strings.TrimSpace(config.BootstrapAddress))
	if err != nil {
		return nil, errors.New("DoH bootstrap address must be an IP and port")
	}
	bootstrapIP, err := netip.ParseAddr(bootstrapHost)
	if err != nil || !IsPublicAddress(bootstrapIP) {
		return nil, errors.New("DoH bootstrap address must use a public IP")
	}
	if port, err := strconv.Atoi(bootstrapPort); err != nil || port < 1 || port > 65535 {
		return nil, errors.New("DoH bootstrap address has an invalid port")
	}
	bootstrapAddress := net.JoinHostPort(bootstrapIP.Unmap().String(), bootstrapPort)
	expectedEndpointAddress := net.JoinHostPort(endpoint.Hostname(), endpointPort)
	dialer := &net.Dialer{Timeout: defaultDoHTimeout, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		Proxy:              nil,
		DisableCompression: true,
		ForceAttemptHTTP2:  true,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			if address != expectedEndpointAddress {
				return nil, errors.New("DoH transport refused an unexpected destination")
			}
			return dialer.DialContext(ctx, network, bootstrapAddress)
		},
		TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
			ServerName: endpoint.Hostname(),
		},
		TLSHandshakeTimeout:   defaultDoHTimeout,
		ResponseHeaderTimeout: defaultDoHTimeout,
		MaxIdleConns:          2,
		MaxIdleConnsPerHost:   2,
		IdleConnTimeout:       30 * time.Second,
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   defaultDoHTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	return &doHResolver{
		endpoint:         endpoint.String(),
		client:           client,
		maxResponseBytes: defaultDoHMaxResponseBytes,
	}, nil
}

func (resolver *doHResolver) LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error) {
	var types []dnsmessage.Type
	switch network {
	case "ip":
		types = []dnsmessage.Type{dnsmessage.TypeA, dnsmessage.TypeAAAA}
	case "ip4":
		types = []dnsmessage.Type{dnsmessage.TypeA}
	case "ip6":
		types = []dnsmessage.Type{dnsmessage.TypeAAAA}
	default:
		return nil, fmt.Errorf("unsupported lookup network %q", network)
	}
	name, err := dnsmessage.NewName(strings.TrimSuffix(strings.TrimSpace(host), ".") + ".")
	if err != nil {
		return nil, fmt.Errorf("invalid DNS name: %w", err)
	}
	addresses := make([]netip.Addr, 0, 2)
	seen := make(map[netip.Addr]struct{})
	for _, questionType := range types {
		resolved, err := resolver.lookup(ctx, name, questionType)
		if err != nil {
			return nil, err
		}
		for _, address := range resolved {
			address = address.Unmap()
			if _, exists := seen[address]; exists {
				continue
			}
			seen[address] = struct{}{}
			addresses = append(addresses, address)
		}
	}
	if len(addresses) == 0 {
		return nil, errors.New("DoH response contained no IP addresses")
	}
	return addresses, nil
}

func (resolver *doHResolver) lookup(ctx context.Context, name dnsmessage.Name, questionType dnsmessage.Type) ([]netip.Addr, error) {
	var idBytes [2]byte
	if _, err := rand.Read(idBytes[:]); err != nil {
		return nil, fmt.Errorf("create DoH query ID: %w", err)
	}
	id := binary.BigEndian.Uint16(idBytes[:])
	question := dnsmessage.Question{Name: name, Type: questionType, Class: dnsmessage.ClassINET}
	builder := dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: id, RecursionDesired: true})
	if err := builder.StartQuestions(); err != nil {
		return nil, err
	}
	if err := builder.Question(question); err != nil {
		return nil, err
	}
	payload, err := builder.Finish()
	if err != nil {
		return nil, err
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, resolver.endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/dns-message")
	request.Header.Set("Content-Type", "application/dns-message")
	response, err := resolver.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("DoH request: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("DoH response status %d", response.StatusCode)
	}
	mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || !strings.EqualFold(mediaType, "application/dns-message") {
		return nil, errors.New("DoH response has an invalid content type")
	}
	responsePayload, err := io.ReadAll(io.LimitReader(response.Body, resolver.maxResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read DoH response: %w", err)
	}
	if int64(len(responsePayload)) > resolver.maxResponseBytes {
		return nil, errors.New("DoH response exceeds budget")
	}

	var parser dnsmessage.Parser
	header, err := parser.Start(responsePayload)
	if err != nil || !header.Response || header.ID != id || header.Truncated || header.RCode != dnsmessage.RCodeSuccess {
		return nil, errors.New("DoH response has an invalid DNS header")
	}
	questions, err := parser.AllQuestions()
	if err != nil || len(questions) != 1 || questions[0] != question {
		return nil, errors.New("DoH response does not match its question")
	}
	answers, err := parser.AllAnswers()
	if err != nil {
		return nil, errors.New("DoH response has invalid answers")
	}
	addresses := make([]netip.Addr, 0, len(answers))
	for _, answer := range answers {
		switch body := answer.Body.(type) {
		case *dnsmessage.AResource:
			if questionType == dnsmessage.TypeA {
				addresses = append(addresses, netip.AddrFrom4(body.A))
			}
		case *dnsmessage.AAAAResource:
			if questionType == dnsmessage.TypeAAAA {
				addresses = append(addresses, netip.AddrFrom16(body.AAAA))
			}
		}
	}
	return addresses, nil
}
