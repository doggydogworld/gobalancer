package config

type Listener struct {
	Addr     string
	Upstream string
}

type Upstream struct {
	Name     string
	Tags     []string
	Backends []string
}

type RateLimit struct {
	TokenRefillPerSecond float64
	MaxTokens            int
}

type Config struct {
	RootCA    []byte
	ServerCrt []byte
	ServerKey []byte
	Listeners []*Listener
	Upstreams []*Upstream
	RateLimit *RateLimit
}
