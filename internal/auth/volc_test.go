package auth

import (
	"context"
	"testing"
	"time"

	"github.com/volcengine/volcengine-go-sdk/service/cr"
	"github.com/volcengine/volcengine-go-sdk/volcengine"
	"github.com/volcengine/volcengine-go-sdk/volcengine/request"
)

type fakeCRClient struct {
	calls int
	out   *cr.GetAuthorizationTokenOutput
}

func (f *fakeCRClient) GetAuthorizationTokenWithContext(ctx volcengine.Context, input *cr.GetAuthorizationTokenInput, opts ...request.Option) (*cr.GetAuthorizationTokenOutput, error) {
	f.calls++
	return f.out, nil
}

func TestVolcProviderCachesToken(t *testing.T) {
	fake := &fakeCRClient{
		out: &cr.GetAuthorizationTokenOutput{
			Username:   volcengine.String("user"),
			Token:      volcengine.String("token"),
			ExpireTime: volcengine.String(time.Now().Add(time.Hour).Format(time.RFC3339)),
		},
	}
	p := &VolcProvider{
		client:        fake,
		registry:      "registrya",
		refreshBefore: 10 * time.Minute,
	}

	user, pass, err := p.BasicAuth(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if user != "user" || pass != "token" {
		t.Fatalf("auth = %q %q", user, pass)
	}
	user, pass, err = p.BasicAuth(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if user != "user" || pass != "token" {
		t.Fatalf("auth = %q %q", user, pass)
	}
	if fake.calls != 1 {
		t.Fatalf("calls = %d", fake.calls)
	}
}
