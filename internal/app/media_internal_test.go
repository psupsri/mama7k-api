package app

import (
	"net/http"
	"testing"
	"time"
)

// The "GET Object" example from the AWS docs (Signature Calculations for the Authorization
// Header: Transferring Payload in a Single Chunk).
func TestSignV4MatchesAWSExample(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "https://examplebucket.s3.amazonaws.com/test.txt", nil)
	req.Header.Set("Range", "bytes=0-9")
	signV4(req, nil, "AKIAIOSFODNN7EXAMPLE", "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY", "us-east-1", "s3",
		time.Date(2013, 5, 24, 0, 0, 0, 0, time.UTC))
	want := "AWS4-HMAC-SHA256 Credential=AKIAIOSFODNN7EXAMPLE/20130524/us-east-1/s3/aws4_request, " +
		"SignedHeaders=host;range;x-amz-content-sha256;x-amz-date, " +
		"Signature=f0e8bdb87c964420e857bd35b5d6ed310bd44f0170aba48dd91039c6036bdb41"
	if got := req.Header.Get("Authorization"); got != want {
		t.Fatalf("Authorization =\n%s\nwant\n%s", got, want)
	}
}

func TestClientIPCountsTrustedHopsFromTheRight(t *testing.T) {
	r, _ := http.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "10.0.0.5:1234"
	r.Header.Add("X-Forwarded-For", "6.6.6.6, 1.2.3.4")
	r.Header.Add("X-Forwarded-For", "10.0.0.1")
	for hops, want := range map[int]string{0: "10.0.0.5", 1: "10.0.0.1", 2: "1.2.3.4", 9: "10.0.0.5"} {
		s := &Server{}
		s.cfg.TrustedProxyHops = hops
		if got := s.clientIP(r); got != want {
			t.Errorf("hops %d: got %s, want %s", hops, got, want)
		}
	}
}

func TestLimiterWindow(t *testing.T) {
	l := newLimiter(2, time.Minute)
	now := time.Now()
	for i, want := range []bool{true, true, false} {
		if ok, _ := l.allow("ip", now); ok != want {
			t.Fatalf("request %d: allowed = %v", i+1, ok)
		}
	}
	if ok, _ := l.allow("other", now); !ok {
		t.Fatal("limit leaked to another key")
	}
	if ok, _ := l.allow("ip", now.Add(time.Minute)); !ok {
		t.Fatal("window did not reset")
	}
}
