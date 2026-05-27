package auth

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/volcengine/volcengine-go-sdk/service/cr"
	"github.com/volcengine/volcengine-go-sdk/volcengine"
	"github.com/volcengine/volcengine-go-sdk/volcengine/request"
	"github.com/volcengine/volcengine-go-sdk/volcengine/session"
)

type VolcConfig struct {
	AccessKey     string
	SecretKey     string
	Region        string
	Endpoint      string
	Registry      string
	RefreshBefore time.Duration
}

type VolcProvider struct {
	client        volcCRClient
	registry      string
	refreshBefore time.Duration

	mu       sync.Mutex
	username string
	token    string
	expires  time.Time
}

type volcCRClient interface {
	GetAuthorizationTokenWithContext(ctx volcengine.Context, input *cr.GetAuthorizationTokenInput, opts ...request.Option) (*cr.GetAuthorizationTokenOutput, error)
}

func NewVolcProvider(cfg VolcConfig) (*VolcProvider, error) {
	if cfg.AccessKey == "" || cfg.SecretKey == "" || cfg.Region == "" || cfg.Registry == "" {
		return nil, errors.New("volc auth requires access key, secret key, region, and registry")
	}
	if cfg.RefreshBefore <= 0 {
		cfg.RefreshBefore = 10 * time.Minute
	}
	sess := session.Must(session.NewSession(volcengine.NewConfig().
		WithRegion(cfg.Region).
		WithAkSk(cfg.AccessKey, cfg.SecretKey).
		WithEndpoint(cfg.Endpoint)))
	return &VolcProvider{
		client:        cr.New(sess),
		registry:      cfg.Registry,
		refreshBefore: cfg.RefreshBefore,
	}, nil
}

func (p *VolcProvider) BasicAuth(ctx context.Context) (string, string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.username != "" && p.token != "" && time.Until(p.expires) > p.refreshBefore {
		return p.username, p.token, nil
	}
	out, err := p.client.GetAuthorizationTokenWithContext(ctx, &cr.GetAuthorizationTokenInput{
		Registry: volcengine.String(p.registry),
	})
	if err != nil {
		return "", "", err
	}
	if out.Username == nil || out.Token == nil || *out.Username == "" || *out.Token == "" {
		return "", "", errors.New("volc GetAuthorizationToken returned empty username or token")
	}
	expires := time.Now().Add(time.Hour)
	if out.ExpireTime != nil && *out.ExpireTime != "" {
		if parsed, err := time.Parse(time.RFC3339, *out.ExpireTime); err == nil {
			expires = parsed
		}
	}
	p.username = *out.Username
	p.token = *out.Token
	p.expires = expires
	return p.username, p.token, nil
}
