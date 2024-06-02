# testcerts

## Root CA

Root CA cert

```
openssl req -x509 -keyout root.key -out root.crt -sha256 -days 3650 -nodes -subj "/CN=Root" -newkey ec -pkeyopt ec_paramgen_curve:prime256v1
```

## Server Cert

TODO: Use pksc12 for server cert

```
openssl req -new -keyout server.key -out server_signing_req.csr -subj "/CN=server" -nodes -config openssl.cnf -newkey ec -pkeyopt ec_paramgen_curve:prime256v1
openssl x509 -req -in server_signing_req.csr -days 3650 -CA root.crt -CAkey root.key -CAcreateserial -out server.crt -extfile openssl.cnf -extensions req_ext
```

## User certificates

These certificates are used to represent test users of the load balancer. They aren't set up with password encryption for ease of use in testing.

SRE Cert
```
openssl req -new -keyout sre.key -out sre_signing_req.csr -subj "/CN=sre/OU=sre" -nodes -newkey ec -pkeyopt ec_paramgen_curve:prime256v1
openssl x509 -req -in sre_signing_req.csr -days 3650 -CA root.crt -CAkey root.key -out sre.crt
```

Webdev Cert
```
openssl req -new -keyout webdev.key -out webdev_signing_req.csr -subj "/CN=webdev/OU=webdev" -nodes -newkey ec -pkeyopt ec_paramgen_curve:prime256v1
openssl x509 -req -in webdev_signing_req.csr -days 3650 -CA root.crt -CAkey root.key -out webdev.crt
```

DBA Cert
```
openssl req -new -keyout dba.key -out dba_signing_req.csr -subj "/CN=dba/OU=dba" -nodes -newkey ec -pkeyopt ec_paramgen_curve:prime256v1
openssl x509 -req -in dba_signing_req.csr -days 3650 -CA root.crt -CAkey root.key -out dba.crt
```

## Bad certs

These certs should not be able to access any upstreams and may not authenticate

Selfsigned cert attempting to impersonate SRE
```
openssl req -x509 -keyout selfsigned.key -out selfsigned.crt -days 3650 -nodes -subj "/CN=sre/OU=sre" -newkey ec -pkeyopt ec_paramgen_curve:prime256v1
```

Bad cert generated with no CN or OU
```
openssl req -new -keyout anonymous.key -out anonymous_signing_req.csr -days 3650 -nodes -newkey ec -pkeyopt ec_paramgen_curve:prime256v1
openssl x509 -req -in anonymous_signing_req.csr -days 3650 -CA root.crt -CAkey root.key -out anonymous.crt
```