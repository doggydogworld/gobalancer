package main

import (
	"context"
	"log"

	_ "embed"

	"github.com/doggydogworld/gobalancer/config"
	"github.com/doggydogworld/gobalancer/srv"
)

//go:embed srv/testcerts/root.crt
var rootCert []byte

//go:embed srv/testcerts/server.crt
var srvCert []byte

//go:embed srv/testcerts/server.key
var srvKey []byte

func main() {
	cfg := &config.Config{
		RootCA:    rootCert,
		ServerCrt: srvCert,
		ServerKey: srvKey,
		Listeners: []*config.Listener{
			{
				Addr:     "127.0.0.1:9000",
				Upstream: "web",
			},
			{
				Addr:     "127.0.0.1:9001",
				Upstream: "db",
			},
		},
		Upstreams: []*config.Upstream{
			{
				Name: "web",
				Tags: []string{"webdev", "sre"},
				Backends: []string{
					"127.0.0.1:8000",
					"127.0.0.1:8001",
					"127.0.0.1:8002",
				},
			},
			{
				Name: "db",
				Tags: []string{"db", "sre"},
				Backends: []string{
					"127.0.0.1:8100",
					"127.0.0.1:8101",
					"127.0.0.1:8102",
				},
			},
		},
		RateLimit: &config.RateLimit{
			MaxTokens:            10,
			TokenRefillPerSecond: 10.0,
		},
	}
	srv, err := srv.NewServerFromCfg(cfg)
	if err != nil {
		log.Fatal(err)
	}
	if err := srv.ListenAndServe(context.Background()); err != nil {
		log.Fatal(err)
	}
}
