# gobalancer

## Listener and Upstreams

The application rely on two main abstractions:
* Listeners: Binds and listens to an address and forwards all requests to an upstream
* Upstreams: Represents a pool of backend servers that will be forwarded to based on a LCU algorithm

As an example configuration for this:

```yaml
# listeners are served by the server library and are linked to upstreams
listeners:
- 
  # The address the listener will bind to
  addr: 127.0.0.1:8001
  # The upstream that the listener will forward to
  # Must be valid name of a configured upstream
  upstream: web
-
  # There can be more than one listener
  addr: 127.0.0.1:8002
  upstream: db
# upstreams hold a pool of backends that the load balancer will forward to
upstreams:
-
  # The name of the upstream. This will be used to link a listener to a set of backends
  name: web
  # Tags will be used for access control
  tags:
  - web
  - prod
  # backends is a list of addresses to forward to
  backends:
  - prod-frontend1.com
  - prod-frontend2.com
```

## Scope

* Forwarder
    * Least connections connection forwarder that tracks the number of connections per upstream.
        * Active connections should be tracked with a counter for each backend. When the connection is closed the counter should be decremented.
        * When forwarding to an upstream, the backend with the lowest amount of active connections in the pool should be chosen.
        * Updating/reading active connection counters should be safe for concurrent use so should use a synchronization primitive such as `sync.Mutex` or channel.
    * Per-client connection rate limiter
* Server
    * Accept and forward connections to upstreams using library
    * mTLS authentication to have the server verify identity of the client and client of the server
    * Simple authorization scheme limiting client usage of upstreams 

Out of scope
* Forwarder
    * Securing forwarded communications. An assumption can be made that we need to be compatible with a wide variety of backend configurations. In this case we can implement the insecure, but still widely used, method of plaintext communication.

## UX

Start the server

```
$ gobalancer
Load balancer ready for connections...
Listening on :
    127.0.0.1:8001 <-> web
    127.0.0.1:8002 <-> db
```

### HTTPS Example
```
$ curl --cacert <CA_CERT> --cert <CLIENT_CERT> --key <CLIENT_CERT_KEY> https://127.0.0.1:8001
```

## Security

Transport:
* The server should support strong protocols, strong ciphers, and ephemeral key exchange
    * Only enabling TLS 1.3 gives us all of the above. If necessary we can further restrict the cipher suites the server uses.

Authentication:
* Authentication will be handled through mTLS
    * Consider not allowing wildcard certificates for auth but this may be handled by authz

Authorization:
* Enforce least privileges and deny by default
* Permissions should be validated on every request
* Use a robust authorization library
* Log all access attempts for auditing purposes 

Out of scope:
* The load balancer will forward on an insecure format to the backends for this solution. However because the load balancer is not opinionated on how the connection to upstream is created or setup just that it satisfies the `net.Conn` interface we could add configuration to fix that.

## Authorization Scheme

For Authorization the app will use a simple implementation of the ABAC model. This means we need to define a set of attributes that we can expect for the client and how attributes (via `tags`) on the upstreams will relate.

For example in the following scenario:
* Users in the `dba` organization should have access to `db` usptreams
* Users in the `webdev`organization should have access to `webdev` ustream
* Users in the `sre` organization should have access to `db`, `web`, `telemetry` upstreams

The app will not implement a method for configuring users and their roles. Instead the app will gather attributes for users from the certificate subject attributes. In this case we will be using the common `OU` attribute.

As an example of this:
* Dave the DBA
    * CN=dave,OU=dba
* Wendy the webdev
    * CN=wendy,OU=webdev
* Sean the SRE
    * CN=sean,OU=sre

One of the benefits of obtaining attributes like this is that we can offload the responsibility of managing user attributes to the CA and not need an extra mechanism for keeping those attributes up to date. A CA can be expected to be more up to date on user roles than a static config file. Another benefit could be that the CA could create a short lived cert granting privileged access to a user. One negative though is that the `OU` could be considered public which could be a vector of attack.

To keep the scope of the app small, access will simply be granted through tags assigned to resources. If a user certificate contains an `OU` that is also present in the `tags` of an upstream, that user will be granted access.

As an example:

```yaml
upstreams:
-
  name: db
  tags:
  - dba
  - sre
-
  name: website
  tags:
  - webdev
  - sre
-
  name: telemetry
  tags:
  - sre
```

## Implementation Details

### Server

Expected API

```
// LoadBalancingServer listens and accepts connections securely and forwards to upstream servers
type LoadBalancingServer interface {
    // ListenAndServe binds to all addresses defined in the listener configuration and forwards to upstreams
    // This will setup listeners that will concurrently accept requests on all defined addresses and pass to lcu forwarder
    // This is a blocking operation and will only return if the context is cancelled (e.g. SIGTERM handling) or an error occurs
    ListenAndServe(ctx context.context) error
}

// Example usage of the LoadBalancingServer
func main() {
    var srv LoadBalancingServer

    // Initialization

    if err := srv.ListenAndServe(ctx.Background()); err != nil {
        panic(err)
    }
}
```

### Forwarder

Expected API

Note that the following is the simplest use case that can be tested. The expectation is for the server to handle secure connections and then pass them off to this library. The library will be unopinionated on how the connections are receieved.

```go
type FwdInfo struct {
    Upstream string
    Conn *net.Conn
    RateLimiterKey string
}

// Forwarder is responsible for forwarding connections to a series of upstreams
type Forwarder interface {
    // Should be safe for concurrent use
    Forward(ctx context.Context, info *FwdInfo) error
}

func main() {
    var fwdr Forwarder
    // ... Forwarder Initialization

    l, err := net.Listen("tcp", "127.0.0.1:8443", conf)
    if err != nil {
        panic(err)
    }
    for {
        conn, err := l.Accept()
        if err != nil {
            panic(err)
        }
        go fwdr.Forward(context.TODO(), &FwdInfo{
            Upstream: "app",
            Conn: conn,
            RateLimiterKey: "user",
        })
    }
}
```

#### Rate Limiting

The forwarder should perform rate limiting on a per-client basis. A good library for this would be [uber-go/ratelimit](https://github.com/uber-go/ratelimit/tree/main). There are other options but this library has a good amount of usage and very simple API. This should be instantiated per client and kept in a hashmap. Make sure that each rate limiter is safe for concurrent use.

The library will be unopinionated on what key is provided for rate limiting on the forwarder but the expectation is that a library wrapping it will provide the `CN` given in an authenticated user certificate.