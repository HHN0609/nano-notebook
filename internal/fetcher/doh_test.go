package fetcher

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/netip"
	"strings"
	"testing"

	"golang.org/x/net/dns/dnsmessage"
)

func TestNewDoHResolverBuildsAPinnedNoProxyClient(t *testing.T) {
	resolver, err := NewDoHResolver(DoHConfig{
		Endpoint: "https://cloudflare-dns.com/dns-query", BootstrapAddress: "1.1.1.1:443",
	})
	if err != nil {
		t.Fatalf("NewDoHResolver: %v", err)
	}
	implementation := resolver.(*doHResolver)
	transport := implementation.client.Transport.(*http.Transport)
	if transport.Proxy != nil || transport.TLSClientConfig.ServerName != "cloudflare-dns.com" {
		t.Fatalf("DoH transport has proxy = %t, SNI = %q", transport.Proxy != nil, transport.TLSClientConfig.ServerName)
	}
	if _, err := transport.DialContext(context.Background(), "tcp", "example.com:443"); err == nil {
		t.Fatal("DoH transport accepted a destination other than its configured endpoint")
	}
	if err := implementation.client.CheckRedirect(&http.Request{}, nil); !errors.Is(err, http.ErrUseLastResponse) {
		t.Fatalf("DoH redirect policy error = %v", err)
	}
}

func TestNewDoHResolverRejectsUnsafeConfiguration(t *testing.T) {
	tests := []DoHConfig{
		{Endpoint: "http://cloudflare-dns.com/dns-query", BootstrapAddress: "1.1.1.1:443"},
		{Endpoint: "https://cloudflare-dns.com/dns-query", BootstrapAddress: "127.0.0.1:443"},
		{Endpoint: "https://cloudflare-dns.com/dns-query", BootstrapAddress: "1.1.1.1"},
	}
	for _, test := range tests {
		if _, err := NewDoHResolver(test); err == nil {
			t.Fatalf("NewDoHResolver(%+v) accepted unsafe configuration", test)
		}
	}
}

func TestDoHResolverLooksUpIPv4AndIPv6WithDNSMessages(t *testing.T) {
	requestedTypes := make([]dnsmessage.Type, 0, 2)
	resolver := &doHResolver{
		endpoint: "https://resolver.test/dns-query",
		client: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			if request.Method != http.MethodPost || request.Header.Get("Content-Type") != "application/dns-message" || request.URL.String() != "https://resolver.test/dns-query" {
				t.Fatalf("unexpected DoH request: %s %s, content-type %q", request.Method, request.URL, request.Header.Get("Content-Type"))
			}
			payload, err := io.ReadAll(request.Body)
			if err != nil {
				t.Fatal(err)
			}
			responsePayload, questionType := doHResponse(t, payload)
			requestedTypes = append(requestedTypes, questionType)
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/dns-message"}},
				Body:       io.NopCloser(strings.NewReader(string(responsePayload))),
				Request:    request,
			}, nil
		})},
		maxResponseBytes: 4096,
	}

	addresses, err := resolver.LookupNetIP(context.Background(), "ip", "example.com")
	if err != nil {
		t.Fatalf("LookupNetIP: %v", err)
	}
	want := []netip.Addr{netip.MustParseAddr("93.184.216.34"), netip.MustParseAddr("2606:4700:4700::1111")}
	if len(addresses) != len(want) || addresses[0] != want[0] || addresses[1] != want[1] {
		t.Fatalf("LookupNetIP addresses = %v, want %v", addresses, want)
	}
	if len(requestedTypes) != 2 || requestedTypes[0] != dnsmessage.TypeA || requestedTypes[1] != dnsmessage.TypeAAAA {
		t.Fatalf("DoH question types = %v, want A then AAAA", requestedTypes)
	}
}

func TestDoHResolverBoundsAndValidatesResponses(t *testing.T) {
	resolver := &doHResolver{
		endpoint: "https://resolver.test/dns-query",
		client: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/plain"}},
				Body:       io.NopCloser(strings.NewReader("not a DNS message")),
				Request:    request,
			}, nil
		})},
		maxResponseBytes: 4,
	}
	if _, err := resolver.LookupNetIP(context.Background(), "ip4", "example.com"); err == nil {
		t.Fatal("LookupNetIP accepted an invalid, oversized DoH response")
	}
}

func doHResponse(t *testing.T, query []byte) ([]byte, dnsmessage.Type) {
	t.Helper()
	var parser dnsmessage.Parser
	header, err := parser.Start(query)
	if err != nil {
		t.Fatal(err)
	}
	questions, err := parser.AllQuestions()
	if err != nil || len(questions) != 1 {
		t.Fatalf("DoH questions = %v, error = %v", questions, err)
	}
	question := questions[0]
	builder := dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: header.ID, Response: true, RecursionAvailable: true})
	if err := builder.StartQuestions(); err != nil {
		t.Fatal(err)
	}
	if err := builder.Question(question); err != nil {
		t.Fatal(err)
	}
	if err := builder.StartAnswers(); err != nil {
		t.Fatal(err)
	}
	resourceHeader := dnsmessage.ResourceHeader{Name: question.Name, Type: question.Type, Class: dnsmessage.ClassINET, TTL: 60}
	switch question.Type {
	case dnsmessage.TypeA:
		if err := builder.AResource(resourceHeader, dnsmessage.AResource{A: [4]byte{93, 184, 216, 34}}); err != nil {
			t.Fatal(err)
		}
	case dnsmessage.TypeAAAA:
		address := netip.MustParseAddr("2606:4700:4700::1111").As16()
		if err := builder.AAAAResource(resourceHeader, dnsmessage.AAAAResource{AAAA: address}); err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatalf("unexpected question type %v", question.Type)
	}
	response, err := builder.Finish()
	if err != nil {
		t.Fatal(err)
	}
	return response, question.Type
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}
